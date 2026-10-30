package gitops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/storage/driver"
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
	cfg := action.NewConfiguration(action.ConfigurationSetLogger(
		slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := cfg.Init(settings.RESTClientGetter(), namespace, "secret"); err != nil {
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
	cpo := action.ChartPathOptions{RepoURL: r.RepoURL, Version: r.Version}
	path, err := cpo.LocateChart(r.Chart, settings)
	if err != nil {
		return fmt.Errorf("fetch chart %s %s: %w", r.Chart, r.Version, err)
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
