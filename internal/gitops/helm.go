package gitops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/storage/driver"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"

	k8s "github.com/chanupen/devops-lab/internal/kube"
)

// sdkHelm drives the Helm v4 SDK against one kubeconfig. Repository config
// and chart cache live under its own home, apart from the user's Helm setup.
type sdkHelm struct {
	kubeconfig string
	home       string
}

func newSDKHelm(kubeconfig, home string) *sdkHelm {
	return &sdkHelm{kubeconfig: kubeconfig, home: home}
}

func (h *sdkHelm) setup(namespace string, log io.Writer) (*action.Configuration, *cli.EnvSettings, error) {
	settings := cli.New()
	settings.KubeConfig = h.kubeconfig
	settings.SetNamespace(namespace)
	settings.RepositoryConfig = filepath.Join(h.home, "repositories.yaml")
	settings.RepositoryCache = filepath.Join(h.home, "cache")
	settings.ContentCache = filepath.Join(h.home, "content")
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
// fetched before (no network at all), else from the repository with a few
// retries, keeping a copy for next time.
func (h *sdkHelm) chart(ctx context.Context, settings *cli.EnvSettings, r HelmRelease, log io.Writer) (string, error) {
	charts := filepath.Join(h.home, "charts")
	if p, ok := cachedChart(charts, r.Chart, r.Version); ok {
		_, _ = fmt.Fprintf(log, "using cached chart %s\n", p)
		return p, nil
	}
	cpo := action.ChartPathOptions{RepoURL: r.RepoURL, Version: r.Version}
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		var p string
		if p, err = cpo.LocateChart(r.Chart, settings); err == nil {
			return keepChart(p, charts, r.Chart, r.Version), nil
		}
		_, _ = fmt.Fprintf(log, "fetch chart %s %s, attempt %d: %v\n", r.Chart, r.Version, attempt, err)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(attempt) * 5 * time.Second):
		}
	}
	return "", fmt.Errorf("fetch chart %s %s: %w", r.Chart, r.Version, err)
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
