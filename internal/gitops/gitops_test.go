package gitops

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/kube"
)

type fakeHelm struct {
	mu          sync.Mutex
	ensured     []HelmRelease
	uninstalled []string
	err         error
}

func (f *fakeHelm) Ensure(_ context.Context, r HelmRelease, _ io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, r)
	return f.err
}

func (f *fakeHelm) Uninstall(_ context.Context, name, ns string, _ io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uninstalled = append(f.uninstalled, ns+"/"+name)
	return nil
}

// coreApps are the children the bootstrap chart renders for stacks.core.
var coreApps = []string{"cert-manager", "core-gateway", "envoy-gateway", "metrics-server"}

// fakeArgo plays the Argo CD controller: it marks apps Synced and Healthy
// against their current source and adds or prunes core's child apps to match
// the root app's stacks.core value. Like Argo CD it reconciles on a timer,
// which keeps its own writes from feeding back into a watch it is reading.
func fakeArgo(ctx context.Context, t *testing.T, dyn dynamic.Interface) {
	t.Helper()
	apps := dyn.Resource(kube.AppGVR).Namespace(Namespace)
	go func() {
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				reconcile(ctx, apps)
			}
		}
	}()
}

func reconcile(ctx context.Context, apps dynamic.ResourceInterface) {
	list, err := apps.List(ctx, metav1.ListOptions{})
	if err != nil {
		return
	}
	coreOn := false
	for i := range list.Items {
		a := &list.Items[i]
		if a.GetName() == RootApp {
			coreOn, _, _ = unstructured.NestedBool(a.Object, "spec", "source", "helm", "valuesObject", "stacks", "core")
		}
		markReady(ctx, apps, a)
	}
	for _, name := range coreApps {
		_, err := apps.Get(ctx, name, metav1.GetOptions{})
		exists := err == nil
		switch {
		case coreOn && !exists:
			child := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
				"metadata": map[string]any{"name": name, "namespace": Namespace, "labels": map[string]any{stackLabel: "core"}},
				"spec":     map[string]any{"source": map[string]any{"path": name}},
			}}
			_, _ = apps.Create(ctx, child, metav1.CreateOptions{})
		case !coreOn && exists:
			_ = apps.Delete(ctx, name, metav1.DeleteOptions{})
		}
	}
}

func markReady(ctx context.Context, apps dynamic.ResourceInterface, a *unstructured.Unstructured) {
	if kube.AppFrom(a).Ready() {
		return
	}
	src, _, _ := unstructured.NestedMap(a.Object, "spec", "source")
	_ = unstructured.SetNestedField(a.Object, map[string]any{
		"sync":   map[string]any{"status": "Synced", "comparedTo": map[string]any{"source": src}},
		"health": map[string]any{"status": "Healthy"},
	}, "status")
	_, _ = apps.Update(ctx, a, metav1.UpdateOptions{})
}

type env struct {
	runner *Runner
	helm   *fakeHelm
	core   *fake.Clientset
	dyn    *dynamicfake.FakeDynamicClient
}

func newEnv(t *testing.T, argo bool, objs ...runtime.Object) *env {
	t.Helper()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{kube.AppGVR: "ApplicationList"}, objs...)
	core := fake.NewClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: Namespace}})
	h := &fakeHelm{}
	r := newRunner(Options{
		Mode:    "local",
		Repo:    config.Repo{URL: "git@github.com:o/r.git", Revision: "main", SSHKeyFile: "~/.ssh/deploy"},
		Timeout: 3 * time.Second,
	}, h, func(io.Writer) (*kube.Clients, error) { return &kube.Clients{Core: core, Dynamic: dyn}, nil })
	r.home = func() (string, error) { return "/home/u", nil }
	r.readFile = func(p string) ([]byte, error) {
		if p == "/home/u/.ssh/deploy" {
			return []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n"), nil
		}
		return nil, errors.New("open " + p + ": no such file")
	}
	if argo {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		fakeArgo(ctx, t, dyn)
	}
	return &env{runner: r, helm: h, core: core, dyn: dyn}
}

func (e *env) root(t *testing.T) *unstructured.Unstructured {
	t.Helper()
	u, err := e.dyn.Resource(kube.AppGVR).Namespace(Namespace).Get(context.Background(), RootApp, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *env) appNames(t *testing.T) []string {
	t.Helper()
	l, err := e.dyn.Resource(kube.AppGVR).Namespace(Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var n []string
	for _, a := range l.Items {
		n = append(n, a.GetName())
	}
	slices.Sort(n)
	return n
}

func TestApplyGitOps(t *testing.T) {
	e := newEnv(t, true)
	var log bytes.Buffer
	if err := e.runner.Apply(context.Background(), "gitops", &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}

	if len(e.helm.ensured) != 1 {
		t.Fatalf("helm ensured %d releases", len(e.helm.ensured))
	}
	rel := e.helm.ensured[0]
	if rel.Name != "argocd" || rel.Namespace != "argocd" || rel.Chart != "argo-cd" || rel.Version != ArgoChartVersion ||
		rel.RepoURL != ArgoChartRepo || rel.Timeout != 3*time.Second {
		t.Errorf("release = %+v", rel)
	}

	s, err := e.core.CoreV1().Secrets(Namespace).Get(context.Background(), RepoSecret, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Labels["argocd.argoproj.io/secret-type"] != "repository" || s.StringData["url"] != "git@github.com:o/r.git" ||
		s.StringData["type"] != "git" || !strings.HasPrefix(s.StringData["sshPrivateKey"], "-----BEGIN OPENSSH") {
		t.Errorf("secret = %+v", s)
	}
	if strings.Contains(log.String(), "fake\n") {
		t.Error("the key's contents reached the log")
	}

	root := e.root(t)
	checks := []struct {
		field []string
		want  any
	}{
		{[]string{"spec", "source", "repoURL"}, "git@github.com:o/r.git"},
		{[]string{"spec", "source", "targetRevision"}, "main"},
		{[]string{"spec", "source", "path"}, "gitops/bootstrap"},
		{[]string{"spec", "source", "helm", "valuesObject", "mode"}, "local"},
		{[]string{"spec", "source", "helm", "valuesObject", "repo", "revision"}, "main"},
		{[]string{"spec", "syncPolicy", "automated", "prune"}, true},
		{[]string{"spec", "syncPolicy", "automated", "allowEmpty"}, true},
	}
	for _, c := range checks {
		got, _, _ := unstructured.NestedFieldNoCopy(root.Object, c.field...)
		if got != c.want {
			t.Errorf("%s = %v, want %v", strings.Join(c.field, "."), got, c.want)
		}
	}
	if !slices.Contains(root.GetFinalizers(), "resources-finalizer.argocd.argoproj.io") {
		t.Errorf("finalizers = %v", root.GetFinalizers())
	}
	if !strings.Contains(log.String(), "apps ready: 1/1") {
		t.Errorf("log lacks the final wait status:\n%s", log.String())
	}
}

func TestApplyGitOpsKeepsEnabledStacks(t *testing.T) {
	existing := rootApp("local", config.Repo{URL: "git@github.com:o/old.git", Revision: "v1"}, map[string]any{"core": true})
	e := newEnv(t, true, existing)
	if err := e.runner.Apply(context.Background(), "gitops", io.Discard); err != nil {
		t.Fatal(err)
	}
	root := e.root(t)
	core, _, _ := unstructured.NestedBool(root.Object, "spec", "source", "helm", "valuesObject", "stacks", "core")
	url, _, _ := unstructured.NestedString(root.Object, "spec", "source", "repoURL")
	if !core || url != "git@github.com:o/r.git" {
		t.Errorf("core = %v, repoURL = %s; want core kept and the repo refreshed", core, url)
	}
}

func TestApplyGitOpsErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(e *env)
		wantErr string
	}{
		{"no repo url", func(e *env) { e.runner.opts.Repo.URL = "" }, "needs repo.url and repo.sshKeyFile"},
		{"no key file setting", func(e *env) { e.runner.opts.Repo.SSHKeyFile = "" }, "needs repo.url and repo.sshKeyFile"},
		{"key file missing", func(e *env) { e.runner.opts.Repo.SSHKeyFile = "/nope" }, "deploy key: open /nope"},
		{"helm fails", func(e *env) { e.helm.err = errors.New("timed out waiting") }, "install Argo CD: timed out waiting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, true)
			tt.mutate(e)
			err := e.runner.Apply(context.Background(), "gitops", io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestSecretUpsertRotatesKey(t *testing.T) {
	e := newEnv(t, true)
	old := repoSecret("git@github.com:o/r.git", []byte("old key"))
	if _, err := e.core.CoreV1().Secrets(Namespace).Create(context.Background(), old, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.runner.Apply(context.Background(), "gitops", io.Discard); err != nil {
		t.Fatal(err)
	}
	s, _ := e.core.CoreV1().Secrets(Namespace).Get(context.Background(), RepoSecret, metav1.GetOptions{})
	if s.StringData["sshPrivateKey"] == "old key" {
		t.Error("existing secret was not updated")
	}
}

func TestCoreStack(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	if err := e.runner.Apply(ctx, "gitops", io.Discard); err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		action    string
		wantApps  []string
		wantValue bool
	}{
		{"apply", append([]string{"cert-manager", "core-gateway", "envoy-gateway", RootApp}, "metrics-server"), true},
		{"apply", append([]string{"cert-manager", "core-gateway", "envoy-gateway", RootApp}, "metrics-server"), true}, // idempotent
		{"destroy", []string{RootApp}, false},
		{"destroy", []string{RootApp}, false},
	}
	for i, s := range steps {
		var log bytes.Buffer
		var err error
		if s.action == "apply" {
			err = e.runner.Apply(ctx, "core", &log)
		} else {
			err = e.runner.Destroy(ctx, "core", &log)
		}
		if err != nil {
			t.Fatalf("step %d %s: %v\n%s", i, s.action, err, log.String())
		}
		got := e.appNames(t)
		want := slices.Clone(s.wantApps)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("step %d %s: apps = %v, want %v", i, s.action, got, want)
		}
		v, _, _ := unstructured.NestedBool(e.root(t).Object, "spec", "source", "helm", "valuesObject", "stacks", "core")
		if v != s.wantValue {
			t.Errorf("step %d: stacks.core = %v, want %v", i, v, s.wantValue)
		}
	}
}

func TestSetStackAddsAllowEmpty(t *testing.T) {
	old := rootApp("local", config.Repo{URL: "git@github.com:o/r.git", Revision: "main"}, map[string]any{"core": true})
	unstructured.RemoveNestedField(old.Object, "spec", "syncPolicy", "automated", "allowEmpty")
	e := newEnv(t, true, old)
	if err := e.runner.Destroy(context.Background(), "core", io.Discard); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := unstructured.NestedBool(e.root(t).Object, "spec", "syncPolicy", "automated", "allowEmpty"); !v {
		t.Error("allowEmpty not set on an existing root app")
	}
}

func TestCoreStackWithoutGitOps(t *testing.T) {
	e := newEnv(t, true)
	if err := e.runner.Apply(context.Background(), "core", io.Discard); err == nil ||
		!strings.Contains(err.Error(), "apply the gitops stack first") {
		t.Fatalf("apply: err = %v", err)
	}
	if err := e.runner.Destroy(context.Background(), "core", io.Discard); err != nil {
		t.Fatalf("destroy with no root app should be a no-op, got %v", err)
	}
}

func TestWaitTimesOutWithStatus(t *testing.T) {
	e := newEnv(t, false) // no controller: apps never become ready
	err := e.runner.Apply(context.Background(), "gitops", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "waiting for lab-bootstrap Unknown/Unknown") ||
		!strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("err = %v", err)
	}
}

func TestDestroyGitOps(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	if err := e.runner.Apply(ctx, "gitops", io.Discard); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := e.runner.Destroy(ctx, "gitops", &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if n := e.appNames(t); len(n) != 0 {
		t.Errorf("apps left: %v", n)
	}
	if !slices.Equal(e.helm.uninstalled, []string{"argocd/argocd"}) {
		t.Errorf("uninstalled = %v", e.helm.uninstalled)
	}
	if _, err := e.core.CoreV1().Namespaces().Get(ctx, Namespace, metav1.GetOptions{}); err == nil {
		t.Error("namespace argocd still exists")
	}
	// A second destroy finds nothing and still succeeds.
	if err := e.runner.Destroy(ctx, "gitops", io.Discard); err != nil {
		t.Fatalf("second destroy: %v", err)
	}
}

func TestAppsDone(t *testing.T) {
	ready := func(name, stack string) kube.App {
		return kube.App{Name: name, Labels: map[string]string{stackLabel: stack}, Sync: "Synced", Health: "Healthy", Current: true}
	}
	root := ready(RootApp, "gitops")
	stale := root
	stale.Current = false
	progressing := ready("cert-manager", "core")
	progressing.Health = "Progressing"
	tests := []struct {
		name       string
		apps       []kube.App
		stack      string
		on         bool
		wantDone   bool
		wantStatus string
	}{
		{"root ready", []kube.App{root}, "", true, true, "apps ready: 1/1"},
		{"root missing", nil, "", true, false, "apps ready: 0/0"},
		{"root compared old spec", []kube.App{stale}, "", true, false,
			"apps ready: 0/1 (waiting for lab-bootstrap Synced/Healthy (comparing new spec))"},
		{"core apps not rendered yet", []kube.App{root}, "core", true, false, "apps ready: 1/1; no core apps yet"},
		{"core app progressing", []kube.App{root, progressing}, "core", true, false,
			"apps ready: 1/2 (waiting for cert-manager Synced/Progressing)"},
		{"core ready", []kube.App{root, ready("cert-manager", "core")}, "core", true, true, "apps ready: 2/2"},
		{"core still present when off", []kube.App{root, ready("cert-manager", "core")}, "core", false, false,
			"apps ready: 2/2; 1 core apps still present"},
		{"core gone", []kube.App{root}, "core", false, true, "apps ready: 1/1"},
		{"stuck root names its error", []kube.App{{Name: RootApp, Sync: "OutOfSync", Health: "Healthy", Current: true,
			Problems: []string{"SyncError: auto-sync will wipe out all resources"}}}, "", true, false,
			"apps ready: 0/1 (waiting for lab-bootstrap OutOfSync/Healthy [SyncError: auto-sync will wipe out all resources])"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done, status := appsDone(tt.apps, tt.stack, tt.on)
			if done != tt.wantDone || status != tt.wantStatus {
				t.Errorf("got %v %q\nwant %v %q", done, status, tt.wantDone, tt.wantStatus)
			}
		})
	}
}

func TestArgoValues(t *testing.T) {
	v := argoValues()
	get := func(path ...string) any {
		var cur any = v
		for _, p := range path {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = m[p]
		}
		return cur
	}
	tests := []struct {
		path []string
		want any
	}{
		{[]string{"dex", "enabled"}, false},
		{[]string{"notifications", "enabled"}, false},
		{[]string{"redis", "image", "repository"}, "docker.io/library/redis"},
		{[]string{"configs", "params", "server.insecure"}, true},
	}
	for _, tt := range tests {
		if got := get(tt.path...); got != tt.want {
			t.Errorf("%s = %v, want %v", strings.Join(tt.path, "."), got, tt.want)
		}
	}
	lua, _ := get("configs", "cm", "resource.customizations.health.argoproj.io_Application").(string)
	if !strings.Contains(lua, "obj.status.health.status") {
		t.Errorf("Application health check missing: %q", lua)
	}
}

func TestExpand(t *testing.T) {
	r := newRunner(Options{}, nil, nil)
	r.home = func() (string, error) { return "/home/u", nil }
	tests := []struct{ in, want string }{
		{"~/.ssh/devops-lab-deploy", "/home/u/.ssh/devops-lab-deploy"},
		{"/etc/key", "/etc/key"},
		{"relative/key", "relative/key"},
		{"~user/key", "~user/key"},
	}
	for _, tt := range tests {
		if got, err := r.expand(tt.in); err != nil || got != tt.want {
			t.Errorf("expand(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestSupports(t *testing.T) {
	r := newRunner(Options{}, nil, nil)
	for stack, want := range map[string]bool{"gitops": true, "core": true, "cluster": false, "obs": false} {
		if r.Supports(stack) != want {
			t.Errorf("Supports(%s) != %v", stack, want)
		}
		if !want {
			if err := r.Apply(context.Background(), stack, io.Discard); err == nil {
				t.Errorf("Apply(%s) should fail", stack)
			}
		}
	}
}
