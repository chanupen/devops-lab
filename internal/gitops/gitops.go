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
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/retry"

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
	// DefaultRevision is what lab-bootstrap and the in-repo apps it renders
	// track; lab up --revision overrides it to test a branch before merging.
	DefaultRevision = "main"
	// StackLabel names the stack an Argo CD Application belongs to.
	StackLabel = "lab.stack"
)

// Argo CD chart, pinned.
const (
	ArgoChartRepo    = "https://argoproj.github.io/argo-helm"
	ArgoChart        = "argo-cd"
	ArgoChartVersion = "10.10.5" // Argo CD v3.5.5
)

// appStacks are the stacks rendered by the bootstrap chart.
var appStacks = []string{"core", "obs", "policy", "secrets", "sample-app", "sample-db"}

// stackNamespaces are created by a stack's apps (CreateNamespace) and are not
// removed by Argo CD, so destroying the stack deletes them.
var stackNamespaces = map[string][]string{
	"core":       {"cert-manager", "envoy-gateway-system"},
	"obs":        {ObsNamespace},
	"policy":     {"kyverno"},
	"secrets":    {"external-secrets"},
	"sample-db":  {"sample-db"},
	"sample-app": {"podinfo"},
}

// ObsNamespace holds the obs stack, including the generated Grafana Secret.
const ObsNamespace = "monitoring"

// GrafanaSecret holds Grafana's admin login, generated in the cluster.
const GrafanaSecret = "grafana-admin"

// NewHelm returns the Helm v4 SDK client the GitOps runner uses, for
// providers that install a chart themselves before GitOps exists (the cloud
// provider's Karpenter and Load Balancer Controller).
// charts is the chart cache the local and cloud labs share.
func NewHelm(kubeconfig, home, charts string) Helm { return newSDKHelm(kubeconfig, home, charts) }

// Helm installs and removes one release. The Helm v4 SDK implements it; tests
// use a fake.
type Helm interface {
	// Ensure installs the release, or upgrades it if it exists, and waits
	// until its resources are ready.
	Ensure(ctx context.Context, r HelmRelease, log io.Writer) error
	// Uninstall removes the release; a missing release is not an error.
	Uninstall(ctx context.Context, name, namespace string, log io.Writer) error
}

// ArgoCDChart is the Argo CD release the gitops stack installs from the
// host, without values: lab up prefetches its chart.
var ArgoCDChart = HelmRelease{Name: Release, Namespace: Namespace, RepoURL: ArgoChartRepo, Chart: ArgoChart, Version: ArgoChartVersion}

// HelmRelease is a chart from a Helm repository, or, with an empty
// RepoURL, an oci:// chart reference.
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
	// Revision is the Git revision to sync; empty means DefaultRevision.
	Revision string
	// Timeout bounds each wait (Argo CD install, apps Synced and Healthy).
	// Image pulls dominate on a slow network.
	Timeout time.Duration
	// ArgoValues are merged over the Argo CD chart values: what the mode's
	// provider needs (cloud mode: memory requests, so Karpenter sizes nodes).
	ArgoValues map[string]any
}

// Runner applies and destroys the gitops and app stacks. It implements
// engine.Runner.
type Runner struct {
	opts Options
	helm Helm
	// clients connects to the cluster; API warnings go to the step log.
	clients  func(log io.Writer) (*kube.Clients, error)
	readFile func(string) ([]byte, error)
	home     func() (string, error)
	password func() (string, error)
}

// New returns a Runner that reaches the cluster through kubeconfig. Chart
// archives go to charts, the cache the local and cloud labs share.
func New(opts Options, kubeconfig, helmHome, charts string) *Runner {
	return newRunner(opts, newSDKHelm(kubeconfig, helmHome, charts), func(log io.Writer) (*kube.Clients, error) {
		cfg, err := kube.RESTConfig(kubeconfig)
		if err != nil {
			return nil, err
		}
		return kube.NewClients(kube.WarningsTo(cfg, log))
	})
}

func newRunner(opts Options, h Helm, clients func(io.Writer) (*kube.Clients, error)) *Runner {
	if opts.Timeout == 0 {
		opts.Timeout = 20 * time.Minute
	}
	if opts.Revision == "" {
		opts.Revision = DefaultRevision
	}
	return &Runner{opts: opts, helm: h, clients: clients, readFile: os.ReadFile, home: os.UserHomeDir, password: randomPassword}
}

// Supports reports whether the runner handles the stack.
func (r *Runner) Supports(stack string) bool {
	return slices.Contains(Stacks(), stack)
}

// Stacks lists the stacks the GitOps runner handles: gitops itself and the
// stacks whose apps it switches on in the app-of-apps.
func Stacks() []string { return append([]string{"gitops"}, appStacks...) }

// overlayModes are the modes with overlays under gitops/overlays.
var overlayModes = []string{"local", "cloud"}

// HasOverlay reports whether the GitOps stacks can run in mode.
func HasOverlay(mode string) bool { return slices.Contains(overlayModes, mode) }

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
	argo := ArgoCDChart
	argo.Values, argo.Timeout = mergeValues(argoValues(), r.opts.ArgoValues), r.opts.Timeout
	if err := r.helm.Ensure(ctx, argo, log); err != nil {
		return fmt.Errorf("install Argo CD: %w", err)
	}
	c, err := r.clients(log)
	if err != nil {
		return err
	}
	logf(log, "loading deploy key %s as Secret %s/%s", keyPath, Namespace, RepoSecret)
	if err := upsertSecret(ctx, c, repoSecret(repo.URL, key)); err != nil {
		return err
	}
	apps := c.Dynamic.Resource(kube.AppGVR).Namespace(Namespace)
	_, err = apps.Get(ctx, RootApp, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		logf(log, "creating Application %s (%s at %s)", RootApp, repo.URL, r.opts.Revision)
		if _, err := apps.Create(ctx, rootApp(r.opts.Mode, repo, r.opts.Revision, map[string]any{}), metav1.CreateOptions{}); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		logf(log, "updating Application %s (%s at %s)", RootApp, repo.URL, r.opts.Revision)
		err := updateRoot(ctx, apps, func(u *unstructured.Unstructured) error {
			// Keep the stacks already turned on and the values stacks set
			// (SetValue); refresh repo, revision and mode.
			old, _, _ := unstructured.NestedMap(u.Object, "spec", "source", "helm", "valuesObject")
			stacks, _, _ := unstructured.NestedMap(old, "stacks")
			spec := rootApp(r.opts.Mode, repo, r.opts.Revision, stacks).Object["spec"]
			u.Object["spec"] = spec
			for k, v := range old {
				if k != "mode" && k != "repo" && k != "stacks" {
					if err := unstructured.SetNestedField(u.Object, v, "spec", "source", "helm", "valuesObject", k); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return r.waitApps(ctx, c, "", true, log)
}

// SetValue sets valuesObject.<key> on the app-of-apps, for a value a stack
// knows only at apply time (sample-db's image digest). applyGitOps keeps it.
func (r *Runner) SetValue(ctx context.Context, key string, value map[string]any, log io.Writer) error {
	c, err := r.clients(log)
	if err != nil {
		return err
	}
	logf(log, "setting %s on Application %s", key, RootApp)
	return updateRoot(ctx, c.Dynamic.Resource(kube.AppGVR).Namespace(Namespace), func(u *unstructured.Unstructured) error {
		return unstructured.SetNestedField(u.Object, value, "spec", "source", "helm", "valuesObject", key)
	})
}

// Value returns the app-of-apps value key (as SetValue set it), or nil when
// it or the app-of-apps does not exist.
func (r *Runner) Value(ctx context.Context, key string) (map[string]any, error) {
	c, err := r.clients(io.Discard)
	if err != nil {
		return nil, err
	}
	u, err := c.Dynamic.Resource(kube.AppGVR).Namespace(Namespace).Get(ctx, RootApp, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v, _, err := unstructured.NestedMap(u.Object, "spec", "source", "helm", "valuesObject", key)
	return v, err
}

// setStack turns a stack on or off in the app-of-apps and waits until Argo CD
// has added (and made healthy) or removed its apps.
func (r *Runner) setStack(ctx context.Context, stack string, on bool, log io.Writer) error {
	c, err := r.clients(log)
	if err != nil {
		return err
	}
	apps := c.Dynamic.Resource(kube.AppGVR).Namespace(Namespace)
	_, err = apps.Get(ctx, RootApp, metav1.GetOptions{})
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
	if on {
		if err := r.prepare(ctx, c, stack, log); err != nil {
			return err
		}
	}
	logf(log, "setting stacks.%s=%v on Application %s", stack, on, RootApp)
	err = updateRoot(ctx, apps, func(u *unstructured.Unstructured) error {
		if err := unstructured.SetNestedField(u.Object, on, "spec", "source", "helm", "valuesObject", "stacks", stack); err != nil {
			return err
		}
		// Roots created before allowEmpty was set could never prune the last stack.
		return unstructured.SetNestedField(u.Object, true, "spec", "syncPolicy", "automated", "allowEmpty")
	})
	if err != nil {
		return err
	}
	if err := r.waitApps(ctx, c, stack, on, log); err != nil || on {
		return err
	}
	return r.deleteNamespaces(ctx, c, stackNamespaces[stack], log)
}

// updateRoot re-reads the app-of-apps, applies mutate and updates it,
// retrying on conflict: Argo CD writes the app's status all the time.
func updateRoot(ctx context.Context, apps dynamic.ResourceInterface, mutate func(*unstructured.Unstructured) error) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		u, err := apps.Get(ctx, RootApp, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if err := mutate(u); err != nil {
			return err
		}
		_, err = apps.Update(ctx, u, metav1.UpdateOptions{})
		return err
	})
}

// prepare creates what a stack needs before its apps sync: the obs stack's
// Grafana admin Secret, generated here so the password is never in Git.
func (r *Runner) prepare(ctx context.Context, c *kube.Clients, stack string, log io.Writer) error {
	if stack != "obs" {
		return nil
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ObsNamespace}}
	if _, err := c.Core.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	secrets := c.Core.CoreV1().Secrets(ObsNamespace)
	if _, err := secrets.Get(ctx, GrafanaSecret, metav1.GetOptions{}); err == nil {
		logf(log, "Secret %s/%s exists; keeping its password", ObsNamespace, GrafanaSecret)
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	pw, err := r.password()
	if err != nil {
		return err
	}
	logf(log, "generating Grafana admin password in Secret %s/%s", ObsNamespace, GrafanaSecret)
	_, err = secrets.Create(ctx, grafanaSecret(pw), metav1.CreateOptions{})
	return err
}

// deleteNamespaces removes namespaces a destroyed stack's apps left behind.
func (r *Runner) deleteNamespaces(ctx context.Context, c *kube.Clients, names []string, log io.Writer) error {
	for _, ns := range names {
		err := c.Core.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		logf(log, "deleting namespace %s", ns)
		wctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
		err = kube.WaitNamespaceGone(wctx, c.Core, ns, log)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// waitApps waits until the root app has compared its current source and every
// Application is Synced and Healthy. With a stack, it also waits until that
// stack's apps exist (on) or are gone (off).
//
// In cloud mode an apply also fails, with the reason, as soon as the
// cluster shows it will not get there (kube.Stuck under StuckRules): a node
// NotReady or a pod unschedulable for 3 minutes, or a container crash
// looping after 3 restarts. Waiting out the 30-minute timeout tells nothing
// more. Local mode keeps waiting: lab namespaces on the main cluster may
// hold a learner's crash-looping pod on purpose.
func (r *Runner) waitApps(ctx context.Context, c *kube.Clients, stack string, on bool, log io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()
	if r.opts.Mode == "cloud" && on && c.Core != nil {
		var stop context.CancelCauseFunc
		ctx, stop = context.WithCancelCause(ctx)
		defer stop(nil)
		go func() {
			if msg := kube.WatchStuck(ctx, c.Core, StuckRules); msg != "" {
				stop(errors.New(msg))
			}
		}()
	}
	err := kube.WaitApps(ctx, c.Dynamic, Namespace, func(apps []kube.App) (bool, string) {
		return appsDone(apps, stack, on)
	}, log)
	if cause := context.Cause(ctx); err != nil && cause != nil && !errors.Is(cause, context.DeadlineExceeded) && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return err
}

// StuckRules is when a cloud apply gives up early. Karpenter launches a
// spot node in one to two minutes, and a new node is NotReady for less
// than one.
var StuckRules = kube.StuckRules{Grace: 3 * time.Minute, Restarts: 3}

// appsDone is the wait condition, kept pure for tests.
func appsDone(apps []kube.App, stack string, on bool) (bool, string) {
	slices.SortFunc(apps, func(a, b kube.App) int { return strings.Compare(a.Name, b.Name) })
	var waiting []string
	rootSeen, stackApps := false, 0
	for _, a := range apps {
		if a.Name == RootApp {
			rootSeen = true
		}
		if stack != "" && a.Labels[StackLabel] == stack {
			stackApps++
		}
		if !a.Ready() {
			w := fmt.Sprintf("%s %s/%s%s", a.Name, orUnknown(a.Sync), orUnknown(a.Health), staleNote(a))
			if len(a.Problems) > 0 {
				w += " [" + strings.Join(a.Problems, "; ") + "]"
			}
			waiting = append(waiting, w)
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
	c, err := r.clients(log)
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
