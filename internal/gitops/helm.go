package gitops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/registry"
	"helm.sh/helm/v4/pkg/storage/driver"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"

	k8s "github.com/chanupen/devops-lab/internal/kube"
)

// sdkHelm drives the Helm v4 SDK against one kubeconfig. Repository config
// lives under its own home, apart from the user's Helm setup. Chart
// archives live in charts, which the local and cloud labs share: a chart
// version never changes, so one download serves both.
type sdkHelm struct {
	kubeconfig string
	home       string
	charts     string // empty: home/charts
	// backoff is the wait before retry n (1-based); nil: chartBackoff.
	backoff func(n int) time.Duration
}

func newSDKHelm(kubeconfig, home, charts string) *sdkHelm {
	return &sdkHelm{kubeconfig: kubeconfig, home: home, charts: charts}
}

// Chart downloads are retried with exponential backoff. Each attempt has
// Helm's own HTTP timeout (120 s). Six attempts ride out a slow CDN for
// about 14 minutes; prefetching before any stack runs means that wait
// costs nothing in the cloud.
const (
	chartAttempts   = 6
	chartFirstWait  = 5 * time.Second
	chartMaxBackoff = time.Minute
)

func chartBackoff(n int) time.Duration {
	d := chartFirstWait << (n - 1)
	if d <= 0 || d > chartMaxBackoff {
		return chartMaxBackoff
	}
	return d
}

func (h *sdkHelm) chartDir() string {
	if h.charts != "" {
		return h.charts
	}
	return filepath.Join(h.home, "charts")
}

func (h *sdkHelm) settings() *cli.EnvSettings {
	settings := cli.New()
	settings.RepositoryConfig = filepath.Join(h.home, "repositories.yaml")
	settings.RepositoryCache = filepath.Join(h.home, "cache")
	settings.ContentCache = filepath.Join(h.home, "content")
	return settings
}

// FetchCharts puts every release's chart into the shared cache, without a
// cluster. lab up calls it before it applies anything, so an unreachable
// chart repository stops the run in seconds with nothing built.
func FetchCharts(ctx context.Context, home, charts string, rs []HelmRelease, log io.Writer) error {
	h := newSDKHelm("", home, charts)
	for _, r := range rs {
		if _, err := h.chart(ctx, h.settings(), r, log); err != nil {
			return err
		}
	}
	return nil
}

func (h *sdkHelm) setup(namespace string, log io.Writer) (*action.Configuration, *cli.EnvSettings, error) {
	settings := h.settings()
	settings.KubeConfig = h.kubeconfig
	settings.SetNamespace(namespace)
	cfg := action.NewConfiguration(action.ConfigurationSetLogger(
		slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelInfo})))
	getter := settings.RESTClientGetter()
	if flags, ok := getter.(*genericclioptions.ConfigFlags); ok {
		flags.WrapConfigFn = func(c *rest.Config) *rest.Config { return k8s.WarningsTo(c, log) }
	}
	if err := cfg.Init(getter, namespace, "secret"); err != nil {
		return nil, nil, err
	}
	return cfg, settings, nil
}

// Ensure implements Helm.
func (h *sdkHelm) Ensure(ctx context.Context, r HelmRelease, log io.Writer) error {
	cfg, settings, err := h.setup(r.Namespace, log)
	if err != nil {
		return err
	}
	path, err := h.chart(ctx, settings, r, log)
	if err != nil {
		return err
	}
	ch, err := loader.Load(path)
	if err != nil {
		return err
	}
	hist := action.NewHistory(cfg)
	hist.Max = 1
	_, err = hist.Run(r.Name)
	switch {
	case errors.Is(err, driver.ErrReleaseNotFound):
		_, _ = fmt.Fprintf(log, "helm install %s %s/%s --version %s\n", r.Name, r.RepoURL, r.Chart, r.Version)
		i := action.NewInstall(cfg)
		i.ReleaseName, i.Namespace, i.CreateNamespace = r.Name, r.Namespace, true
		i.WaitStrategy, i.Timeout = kube.StatusWatcherStrategy, r.Timeout
		_, err = i.RunWithContext(ctx, ch, r.Values)
	case err != nil:
		return err
	default:
		_, _ = fmt.Fprintf(log, "helm upgrade %s %s/%s --version %s\n", r.Name, r.RepoURL, r.Chart, r.Version)
		u := action.NewUpgrade(cfg)
		u.Namespace, u.WaitStrategy, u.Timeout, u.MaxHistory = r.Namespace, kube.StatusWatcherStrategy, r.Timeout, 5
		_, err = u.RunWithContext(ctx, r.Name, ch, r.Values)
	}
	return err
}

// chart returns the chart archive: the lab's own copy when this version was
// fetched before (no network at all), else from the repository, retried
// with backoff, keeping a copy for next time.
func (h *sdkHelm) chart(ctx context.Context, settings *cli.EnvSettings, r HelmRelease, log io.Writer) (string, error) {
	return h.fetch(ctx, r, log, func() (string, error) { return h.locate(settings, r, log) })
}

// fetch serves the cache or calls locate until it succeeds, at most
// chartAttempts times.
func (h *sdkHelm) fetch(ctx context.Context, r HelmRelease, log io.Writer, locate func() (string, error)) (string, error) {
	charts := h.chartDir()
	name := path.Base(r.Chart) // oci://registry/repo/name -> name
	if p, ok := cachedChart(charts, name, r.Version); ok {
		_, _ = fmt.Fprintf(log, "using cached chart %s\n", p)
		return p, nil
	}
	backoff := h.backoff
	if backoff == nil {
		backoff = chartBackoff
	}
	var err error
	for attempt := 1; attempt <= chartAttempts; attempt++ {
		var p string
		if p, err = locate(); err == nil {
			_, _ = fmt.Fprintf(log, "fetched chart %s %s\n", r.Chart, r.Version)
			return keepChart(p, charts, name, r.Version), nil
		}
		if attempt == chartAttempts {
			break
		}
		wait := backoff(attempt)
		_, _ = fmt.Fprintf(log, "fetch chart %s %s, attempt %d of %d: %v; retrying in %s\n",
			r.Chart, r.Version, attempt, chartAttempts, err, wait)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
	}
	return "", fmt.Errorf("fetch chart %s %s after %d attempts: %w", r.Chart, r.Version, chartAttempts, err)
}

// locate downloads the chart with the Helm SDK.
func (h *sdkHelm) locate(settings *cli.EnvSettings, r HelmRelease, log io.Writer) (string, error) {
	// An Install carries the chart path options plus, for oci:// charts,
	// the registry client LocateChart needs (anonymous pulls).
	loc := action.NewInstall(&action.Configuration{})
	loc.RepoURL, loc.Version = r.RepoURL, r.Version
	if registry.IsOCI(r.Chart) {
		rc, err := registry.NewClient(registry.ClientOptWriter(log),
			registry.ClientOptCredentialsFile(filepath.Join(h.home, "registry.json")))
		if err != nil {
			return "", err
		}
		loc.SetRegistryClient(rc)
	}
	return loc.LocateChart(r.Chart, settings)
}

// keepChart copies a downloaded archive to dir/<chart>-<version>.tgz and
// returns the copy's path; on any error it returns src unchanged.
func keepChart(src, dir, chart, version string) string {
	b, err := os.ReadFile(src)
	if err != nil {
		return src
	}
	dst := filepath.Join(dir, chart+"-"+version+".tgz")
	if os.MkdirAll(dir, 0o700) != nil || os.WriteFile(dst+".tmp", b, 0o600) != nil || os.Rename(dst+".tmp", dst) != nil {
		return src
	}
	return dst
}

// cachedChart reports the lab's copy of chart at version, if any.
func cachedChart(cache, chart, version string) (string, bool) {
	p := filepath.Join(cache, chart+"-"+version+".tgz")
	if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
		return p, true
	}
	return "", false
}

// Uninstall implements Helm.
func (h *sdkHelm) Uninstall(_ context.Context, name, namespace string, log io.Writer) error {
	cfg, _, err := h.setup(namespace, log)
	if err != nil {
		return err
	}
	u := action.NewUninstall(cfg)
	u.IgnoreNotFound, u.WaitStrategy = true, kube.StatusWatcherStrategy
	_, _ = fmt.Fprintf(log, "helm uninstall %s -n %s\n", name, namespace)
	_, err = u.Run(name)
	return err
}
