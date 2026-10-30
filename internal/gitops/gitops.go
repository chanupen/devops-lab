// Package gitops runs the stacks that live in the cluster: gitops installs
// Argo CD and the app-of-apps, and every later stack (core, ...) is turned on
// by setting stacks.<name>=true on that app-of-apps. It is the same in every
// mode; mode differences live in gitops/overlays.
package gitops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/kube"
)

// Names of the objects the gitops stack owns.
const (
	Namespace     = "argocd"
	Release       = "argocd"
	RootApp       = "lab-bootstrap"
	RepoSecret    = "lab-repo"
	BootstrapPath = "gitops/bootstrap"
	stackLabel    = "lab.stack"
)

// Argo CD chart, pinned.
const (
	ArgoChartRepo    = "https://argoproj.github.io/argo-helm"
	ArgoChart        = "argo-cd"
	ArgoChartVersion = "10.10.5" // Argo CD v3.5.5
)

// appStacks are the stacks rendered by the bootstrap chart.
var appStacks = []string{"core"}

// Helm installs and removes one release. The Helm v4 SDK implements it; tests
// use a fake.
type Helm interface {
	// Ensure installs the release, or upgrades it if it exists, and waits
	// until its resources are ready.
	Ensure(ctx context.Context, r HelmRelease, log io.Writer) error
	// Uninstall removes the release; a missing release is not an error.
	Uninstall(ctx context.Context, name, namespace string, log io.Writer) error
}

// HelmRelease is a chart from a Helm repository.
type HelmRelease struct {
	Name, Namespace         string
	RepoURL, Chart, Version string
	Values                  map[string]any
	Timeout                 time.Duration
}

// Options configure a Runner.
type Options struct {
	Mode string
	Repo config.Repo
	// Timeout bounds each wait (Argo CD install, apps Synced and Healthy).
	// Image pulls dominate on a slow network.
	Timeout time.Duration
}

// Runner applies and destroys the gitops and app stacks. It implements
// engine.Runner.
type Runner struct {
	opts     Options
	helm     Helm
	clients  func() (*kube.Clients, error)
	readFile func(string) ([]byte, error)
	home     func() (string, error)
}

// New returns a Runner that reaches the cluster through kubeconfig.
func New(opts Options, kubeconfig, helmHome string) *Runner {
	return newRunner(opts, newSDKHelm(kubeconfig, helmHome), func() (*kube.Clients, error) {
		cfg, err := kube.RESTConfig(kubeconfig)
		if err != nil {
			return nil, err
		}
		return kube.NewClients(cfg)
	})
}

func newRunner(opts Options, h Helm, clients func() (*kube.Clients, error)) *Runner {
	if opts.Timeout == 0 {
		opts.Timeout = 20 * time.Minute
	}
	return &Runner{opts: opts, helm: h, clients: clients, readFile: os.ReadFile, home: os.UserHomeDir}
}

// Supports reports whether the runner handles the stack.
func (r *Runner) Supports(stack string) bool {
	return stack == "gitops" || slices.Contains(appStacks, stack)
}

// Apply implements engine.Runner.
func (r *Runner) Apply(ctx context.Context, stack string, log io.Writer) error {
	switch {
	case stack == "gitops":
		return r.applyGitOps(ctx, log)
	case slices.Contains(appStacks, stack):
		return r.setStack(ctx, stack, true, log)
	}
	return fmt.Errorf("gitops runner does not handle stack %q", stack)
}

// Destroy implements engine.Runner.
func (r *Runner) Destroy(ctx context.Context, stack string, log io.Writer) error {
	switch {
	case stack == "gitops":
		return r.destroyGitOps(ctx, log)
	case slices.Contains(appStacks, stack):
		return r.setStack(ctx, stack, false, log)
	}
	return fmt.Errorf("gitops runner does not handle stack %q", stack)
}

// applyGitOps installs Argo CD, loads the deploy key as a repository Secret,
// creates or updates the app-of-apps, and waits until every app is ready.
func (r *Runner) applyGitOps(ctx context.Context, log io.Writer) error {
	repo := r.opts.Repo
	if repo.URL == "" || repo.SSHKeyFile == "" {
		return errors.New("lab.yaml needs repo.url and repo.sshKeyFile for the gitops stack")
	}
	keyPath, err := r.expand(repo.SSHKeyFile)
	if err != nil {
		return err
	}
	key, err := r.readFile(keyPath)
	if err != nil {
		return fmt.Errorf("deploy key: %w", err)
	}
	logf(log, "installing Argo CD (chart %s %s)", ArgoChart, ArgoChartVersion)
	if err := r.helm.Ensure(ctx, HelmRelease{
		Name: Release, Namespace: Namespace,
		RepoURL: ArgoChartRepo, Chart: ArgoChart, Version: ArgoChartVersion,
		Values: argoValues(), Timeout: r.opts.Timeout,
	}, log); err != nil {
		return fmt.Errorf("install Argo CD: %w", err)
	}
	c, err := r.clients()
	if err != nil {
		return err
	}
	logf(log, "loading deploy key %s as Secret %s/%s", keyPath, Namespace, RepoSecret)
	if err := upsertSecret(ctx, c, repoSecret(repo.URL, key)); err != nil {
		return err
	}
	apps := c.Dynamic.Resource(kube.AppGVR).Namespace(Namespace)
	existing, err := apps.Get(ctx, RootApp, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		logf(log, "creating Application %s (%s at %s)", RootApp, repo.URL, repo.Revision)
		if _, err := apps.Create(ctx, rootApp(r.opts.Mode, repo, map[string]any{}), metav1.CreateOptions{}); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		// Keep the stacks already turned on; refresh repo and mode.
		stacks, _, _ := unstructured.NestedMap(existing.Object, "spec", "source", "helm", "valuesObject", "stacks")
		want := rootApp(r.opts.Mode, repo, stacks)
		existing.Object["spec"] = want.Object["spec"]
		logf(log, "updating Application %s", RootApp)
		if _, err := apps.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	return r.waitApps(ctx, c, "", true, log)
}

// setStack turns a stack on or off in the app-of-apps and waits until Argo CD
// has added (and made healthy) or removed its apps.
func (r *Runner) setStack(ctx context.Context, stack string, on bool, log io.Writer) error {
	c, err := r.clients()
	if err != nil {
		return err
	}
	apps := c.Dynamic.Resource(kube.AppGVR).Namespace(Namespace)
	root, err := apps.Get(ctx, RootApp, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if !on {
			logf(log, "Application %s is gone; nothing to remove", RootApp)
			return nil
		}
		return fmt.Errorf("app-of-apps %s not found; apply the gitops stack first", RootApp)
	}
	if err != nil {
		return err
	}
	if err := unstructured.SetNestedField(root.Object, on, "spec", "source", "helm", "valuesObject", "stacks", stack); err != nil {
		return err
	}
	logf(log, "setting stacks.%s=%v on Application %s", stack, on, RootApp)
	if _, err := apps.Update(ctx, root, metav1.UpdateOptions{}); err != nil {
		return err
	}
	return r.waitApps(ctx, c, stack, on, log)
}

// waitApps waits until the root app has compared its current source and every
// Application is Synced and Healthy. With a stack, it also waits until that
// stack's apps exist (on) or are gone (off).
func (r *Runner) waitApps(ctx context.Context, c *kube.Clients, stack string, on bool, log io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()
	return kube.WaitApps(ctx, c.Dynamic, Namespace, func(apps []kube.App) (bool, string) {
		return appsDone(apps, stack, on)
	}, log)
}

// appsDone is the wait condition, kept pure for tests.
func appsDone(apps []kube.App, stack string, on bool) (bool, string) {
	slices.SortFunc(apps, func(a, b kube.App) int { return strings.Compare(a.Name, b.Name) })
	var waiting []string
	rootSeen, stackApps := false, 0
	for _, a := range apps {
		if a.Name == RootApp {
			rootSeen = true
		}
		if stack != "" && a.Labels[stackLabel] == stack {
			stackApps++
		}
		if !a.Ready() {
			waiting = append(waiting, fmt.Sprintf("%s %s/%s%s", a.Name, orUnknown(a.Sync), orUnknown(a.Health), staleNote(a)))
		}
	}
	status := fmt.Sprintf("apps ready: %d/%d", len(apps)-len(waiting), len(apps))
	if len(waiting) > 0 {
		status += " (waiting for " + strings.Join(waiting, ", ") + ")"
	}
	done := rootSeen && len(waiting) == 0
	if stack != "" {
		if on && stackApps == 0 {
			done, status = false, status+fmt.Sprintf("; no %s apps yet", stack)
		}
		if !on && stackApps > 0 {
			done, status = false, status+fmt.Sprintf("; %d %s apps still present", stackApps, stack)
		}
	}
	return done, status
}

func orUnknown(s string) string {
	if s == "" {
		return "Unknown"
	}
	return s
}

func staleNote(a kube.App) string {
	if a.Sync != "" && !a.Current {
		return " (comparing new spec)"
	}
	return ""
}

// destroyGitOps deletes the app-of-apps (its finalizer removes child apps and
// their resources), uninstalls Argo CD and deletes its namespace.
func (r *Runner) destroyGitOps(ctx context.Context, log io.Writer) error {
	c, err := r.clients()
	if err != nil {
		return err
	}
	apps := c.Dynamic.Resource(kube.AppGVR).Namespace(Namespace)
	err = apps.Delete(ctx, RootApp, metav1.DeleteOptions{})
	switch {
	case apierrors.IsNotFound(err):
		logf(log, "Application %s is already gone", RootApp)
	case err != nil:
		return err
	default:
		logf(log, "deleting Application %s and waiting for its apps to go", RootApp)
		wctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
		err = kube.WaitApps(wctx, c.Dynamic, Namespace, func(apps []kube.App) (bool, string) {
			return len(apps) == 0, fmt.Sprintf("apps left: %d", len(apps))
		}, log)
		cancel()
		if err != nil {
			return err
		}
	}
	logf(log, "uninstalling Argo CD")
	if err := r.helm.Uninstall(ctx, Release, Namespace, log); err != nil {
		return err
	}
	logf(log, "deleting namespace %s", Namespace)
	err = c.Core.CoreV1().Namespaces().Delete(ctx, Namespace, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()
	return kube.WaitNamespaceGone(wctx, c.Core, Namespace, log)
}

func upsertSecret(ctx context.Context, c *kube.Clients, s *corev1.Secret) error {
	secrets := c.Core.CoreV1().Secrets(s.Namespace)
	_, err := secrets.Create(ctx, s, metav1.CreateOptions{})
	if !apierrors.IsAlreadyExists(err) {
		return err
	}
	cur, err := secrets.Get(ctx, s.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	cur.Labels, cur.Data, cur.StringData = s.Labels, nil, s.StringData
	_, err = secrets.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

// expand resolves a leading ~/ against the home directory.
func (r *Runner) expand(path string) (string, error) {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path, nil
	}
	home, err := r.home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, rest), nil
}

func logf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format+"\n", a...) }
