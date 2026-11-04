package drills

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/chanupen/devops-lab/internal/state"
)

var (
	deployGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	appGVR    = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}
)

func deployment(ns, name string, replicas int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": name, "namespace": ns, "labels": map[string]any{"k8s-app": name},
			"annotations": map[string]any{"deployment.kubernetes.io/revision": "1"}},
		"spec":   map[string]any{"replicas": replicas},
		"status": map[string]any{"readyReplicas": replicas},
	}}
}

func application(name string, params []any) *unstructured.Unstructured {
	helm := map[string]any{"releaseName": name}
	if params != nil {
		helm["parameters"] = params
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]any{"name": name, "namespace": "argocd", "annotations": map[string]any{"argocd.argoproj.io/sync-wave": "0"}},
		"spec": map[string]any{"source": map[string]any{"helm": helm},
			"syncPolicy": map[string]any{"automated": map[string]any{"selfHeal": true}}},
	}}
}

func fakeCluster(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{deployGVR: "DeploymentList", appGVR: "ApplicationList"}, objs...)
}

var (
	corednsRef = Ref{APIVersion: "apps/v1", Resource: "deployments", Namespace: "kube-system", Name: "coredns"}
	appRef     = Ref{APIVersion: "argoproj.io/v1alpha1", Resource: "applications", Namespace: "argocd", Name: "podinfo-frontend"}
	jobRef     = Ref{APIVersion: "apps/v1", Resource: "deployments", Namespace: "podinfo", Name: "made-by-the-drill"}
)

func get(t *testing.T, dyn dynamic.Interface, g schema.GroupVersionResource, ns, name string) *unstructured.Unstructured {
	t.Helper()
	u, err := dyn.Resource(g).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// The fault changes the saved objects every way a drill might: scales
// CoreDNS to zero and relabels it, adds Helm parameters and drops
// auto-sync on an Application, and creates an object that was not there.
// Restore puts each back; Diff sees the difference before and none after.
func TestCaptureRestoreDiff(t *testing.T) {
	ctx := context.Background()
	dyn := fakeCluster(deployment("kube-system", "coredns", 2), application("podinfo-frontend", nil))
	saved, err := Capture(ctx, dyn, []Ref{corednsRef, appRef, jobRef})
	if err != nil {
		t.Fatal(err)
	}
	if !saved[2].Absent || saved[0].Absent {
		t.Fatalf("absent flags: %+v", saved)
	}
	if bytes.Contains(saved[0].Object, []byte("readyReplicas")) {
		t.Error("status was saved")
	}
	// The fault.
	d := get(t, dyn, deployGVR, "kube-system", "coredns")
	_ = unstructured.SetNestedField(d.Object, int64(0), "spec", "replicas")
	d.SetLabels(map[string]string{"k8s-app": "coredns", "scaled-by": "cost-saver"})
	d.SetAnnotations(map[string]string{"deployment.kubernetes.io/revision": "2"})
	if _, err := dyn.Resource(deployGVR).Namespace("kube-system").Update(ctx, d, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	a := get(t, dyn, appGVR, "argocd", "podinfo-frontend")
	_ = unstructured.SetNestedSlice(a.Object, []any{map[string]any{"name": "image.tag", "value": "bad"}}, "spec", "source", "helm", "parameters")
	unstructured.RemoveNestedField(a.Object, "spec", "syncPolicy", "automated")
	a.Object["operation"] = map[string]any{"sync": map[string]any{}}
	a.SetAnnotations(map[string]string{"argocd.argoproj.io/sync-wave": "0", "argocd.argoproj.io/hydrate": "normal"})
	if _, err := dyn.Resource(appGVR).Namespace("argocd").Update(ctx, a, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := dyn.Resource(deployGVR).Namespace("podinfo").Create(ctx, deployment("podinfo", "made-by-the-drill", 1), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	diffs, err := Diff(ctx, dyn, saved)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"deployments/coredns: metadata.labels, spec differ",
		"applications/podinfo-frontend: metadata.annotations, spec differ",
		"deployments/made-by-the-drill: exists, but did not before the drill",
	}
	if strings.Join(diffs, "\n") != strings.Join(want, "\n") {
		t.Errorf("diff before restore:\n%s\nwant\n%s", strings.Join(diffs, "\n"), strings.Join(want, "\n"))
	}

	if err := Restore(ctx, dyn, saved); err != nil {
		t.Fatal(err)
	}
	if diffs, err := Diff(ctx, dyn, saved); err != nil || len(diffs) > 0 {
		t.Fatalf("after restore: %v, %v", diffs, err)
	}
	d = get(t, dyn, deployGVR, "kube-system", "coredns")
	if r, _, _ := unstructured.NestedInt64(d.Object, "spec", "replicas"); r != 2 {
		t.Errorf("replicas = %d", r)
	}
	if d.GetAnnotations()["deployment.kubernetes.io/revision"] != "2" {
		t.Error("restore changed an annotation the Deployment controller owns")
	}
	if _, ok := get(t, dyn, appGVR, "argocd", "podinfo-frontend").Object["operation"]; ok {
		t.Error("the Application kept the drill's operation")
	}
	// Idempotent: a second restore (after a crash halfway) changes nothing.
	if err := Restore(ctx, dyn, saved); err != nil {
		t.Fatal(err)
	}
	// An object deleted by the fault is created again.
	if err := dyn.Resource(deployGVR).Namespace("kube-system").Delete(ctx, "coredns", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, dyn, saved); err != nil {
		t.Fatal(err)
	}
	if diffs, _ := Diff(ctx, dyn, saved); len(diffs) > 0 {
		t.Errorf("after re-creating: %v", diffs)
	}
}

// fakeRunner runs the drill's scripts as Go functions.
type fakeRunner struct {
	scripts map[string]func() (string, error) // by script base name
	ran     []string
}

func (f *fakeRunner) Run(_ context.Context, script, _ string, env []string) (string, error) {
	name := strings.TrimSuffix(filepath.Base(script), ".sh")
	f.ran = append(f.ran, name)
	if !strings.HasPrefix(env[0], "KUBECONFIG=") {
		return "", errors.New("no KUBECONFIG")
	}
	if fn := f.scripts[name]; fn != nil {
		return fn()
	}
	return "", nil
}

func exitErr(t *testing.T) error {
	t.Helper()
	err := exec.Command("false").Run()
	if err == nil {
		t.Fatal("false succeeded")
	}
	return err
}

type fakeTimer struct{ scheduled, cancelled []string }

func (f *fakeTimer) Schedule(_ context.Context, run string, _ time.Time) (string, error) {
	f.scheduled = append(f.scheduled, run)
	return "lab-drill-" + run, nil
}
func (f *fakeTimer) Cancel(_ context.Context, name string) error {
	f.cancelled = append(f.cancelled, name)
	return nil
}

type fakeAlerts struct{ at time.Time }

func (f fakeAlerts) FiredAt(context.Context, Alert, time.Time) (time.Time, bool, error) {
	return f.at, !f.at.IsZero(), nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time               { return c.t }
func (c *clock) advance(d time.Duration)      { c.t = c.t.Add(d) }
func (c *clock) at(d time.Duration) time.Time { return c.t.Add(d) }

// testDrill is a dns-like drill on a fake cluster: inject scales CoreDNS
// to zero, the solution scales it back.
func testSetup(t *testing.T) (*Manager, *dynamicfake.FakeDynamicClient, *fakeRunner, *clock) {
	t.Helper()
	dyn := fakeCluster(deployment("kube-system", "coredns", 2))
	scale := func(n int64) func() (string, error) {
		return func() (string, error) {
			d, err := dyn.Resource(deployGVR).Namespace("kube-system").Get(context.Background(), "coredns", metav1.GetOptions{})
			if err != nil {
				return "", err
			}
			_ = unstructured.SetNestedField(d.Object, n, "spec", "replicas")
			_, err = dyn.Resource(deployGVR).Namespace("kube-system").Update(context.Background(), d, metav1.UpdateOptions{})
			return "", err
		}
	}
	r := &fakeRunner{scripts: map[string]func() (string, error){"inject": scale(0), "solution": scale(2)}}
	r.scripts["verify"] = func() (string, error) {
		d, _ := dyn.Resource(deployGVR).Namespace("kube-system").Get(context.Background(), "coredns", metav1.GetOptions{})
		if n, _, _ := unstructured.NestedInt64(d.Object, "spec", "replicas"); n == 0 {
			return "checking CoreDNS\nCoreDNS has 0 replicas\n", exitErr(t)
		}
		return "CoreDNS answers\n", nil
	}
	c := &clock{t: time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)}
	store := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.Stacks = map[string]state.StackState{"sample-app": {Status: state.Applied}, "obs": {Status: state.Applied}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	d := Drill{ID: "dns", Title: "dns", Stacks: []string{"sample-app"}, TimeLimit: 30 * time.Minute, Dir: t.TempDir(),
		Alert: Alert{Name: "CoreDNSDown"}, Hints: []string{"look at kube-system", "look at the replicas"}, Snapshot: []Ref{corednsRef}}
	m := &Manager{Drills: []Drill{d}, Store: store, Kube: func() (dynamic.Interface, error) { return dyn, nil },
		Kubeconfig: "/kc", Runner: r, Now: c.now}
	return m, dyn, r, c
}

func replicas(t *testing.T, dyn dynamic.Interface) int64 {
	t.Helper()
	n, _, _ := unstructured.NestedInt64(get(t, dyn, deployGVR, "kube-system", "coredns").Object, "spec", "replicas")
	return n
}

// A whole run: start, the timers, a failed verify, the fix, a passing
// verify that restores and records the times.
func TestDrillRun(t *testing.T) {
	ctx := context.Background()
	m, dyn, runner, c := testSetup(t)
	timer := &fakeTimer{}
	m.Timer = timer
	m.Alerts = fakeAlerts{at: c.at(90 * time.Second)}
	run, err := m.Start(ctx, "dns", false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase != state.DrillRunning || !run.Started.Equal(c.t) || !run.Deadline.Equal(c.at(30*time.Minute)) {
		t.Fatalf("run = %+v", run)
	}
	if replicas(t, dyn) != 0 || len(timer.scheduled) != 1 {
		t.Fatalf("fault not in (%d replicas) or no timer (%v)", replicas(t, dyn), timer.scheduled)
	}
	if _, err := m.Start(ctx, "dns", false, io.Discard); err == nil || !strings.Contains(err.Error(), "one at a time") {
		t.Errorf("second start: %v", err)
	}
	c.advance(2 * time.Minute)
	if r, err := m.Ack(ctx); err != nil || r.TimeTo(r.Acked) != 2*time.Minute || r.TimeTo(r.AlertFired) != 90*time.Second {
		t.Fatalf("ack: %+v, %v", r, err)
	}
	if n, total, h, err := m.Hint(); err != nil || n != 1 || total != 2 || h != "look at kube-system" {
		t.Errorf("hint = %d/%d %q, %v", n, total, h, err)
	}
	c.advance(time.Minute)
	res, err := m.Verify(ctx, io.Discard)
	if err != nil || res.Passed || res.Reason != "CoreDNS has 0 replicas" {
		t.Fatalf("verify before the fix: %+v, %v", res, err)
	}
	if _, err := runner.scripts["solution"](); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	res, err = m.Verify(ctx, io.Discard)
	if err != nil || !res.Passed {
		t.Fatalf("verify after the fix: %+v, %v", res, err)
	}
	r := res.Run
	if r.Outcome != state.DrillRecovered || r.TimeTo(r.Recovered) != 4*time.Minute || r.Verifies != 2 || r.Hints != 1 || r.Snapshot != nil {
		t.Errorf("finished run = %+v", r)
	}
	if len(timer.cancelled) != 1 {
		t.Errorf("timer not cancelled: %v", timer.cancelled)
	}
	st, _ := m.Store.Load()
	if st.ActiveDrill != nil || len(st.Drills) != 1 {
		t.Fatalf("state: active %+v, history %d", st.ActiveDrill, len(st.Drills))
	}
	if b := BestTimes(st.Drills)["dns"]; b.Fixed != 4*time.Minute || b.Detect != 2*time.Minute || b.Alert != 90*time.Second {
		t.Errorf("best = %+v", b)
	}
	if strings.Join(runner.ran, " ") != "inject verify verify restore" {
		t.Errorf("scripts run: %v", runner.ran)
	}
}

// lab crashes (or the host reboots) while the fault is going in: the
// state file still has the snapshot, so a new lab process restores it.
func TestRestoreAfterCrash(t *testing.T) {
	ctx := context.Background()
	m, dyn, runner, _ := testSetup(t)
	m.Timer = &fakeTimer{}
	inject := runner.scripts["inject"]
	runner.scripts["inject"] = func() (string, error) {
		if _, err := inject(); err != nil {
			return "", err
		}
		panic("lab was killed during inject.sh")
	}
	func() {
		defer func() { _ = recover() }()
		_, _ = m.Start(ctx, "dns", false, io.Discard)
	}()
	if replicas(t, dyn) != 0 {
		t.Fatal("fault not injected")
	}
	st, _ := m.Store.Load()
	if st.ActiveDrill == nil || st.ActiveDrill.Phase != state.DrillInjecting || len(st.ActiveDrill.Snapshot) != 1 {
		t.Fatalf("state after the crash: %+v", st.ActiveDrill)
	}
	if st.ActiveDrill.Timer != "lab-drill-"+st.ActiveDrill.Run {
		t.Errorf("the timer is not in the state before inject: %q", st.ActiveDrill.Timer)
	}
	// A new process: only the state file is shared.
	timer := &fakeTimer{}
	m2 := &Manager{Drills: m.Drills, Store: state.NewStore(m.Store.Path()), Kube: m.Kube, Kubeconfig: "/kc",
		Runner: &fakeRunner{}, Now: m.Now, Timer: timer}
	r, ok, err := m2.RestoreForUp(ctx, io.Discard)
	if err != nil || !ok || r.Outcome != state.DrillRestoredByUp {
		t.Fatalf("RestoreForUp = %+v, %v, %v", r, ok, err)
	}
	if replicas(t, dyn) != 2 {
		t.Errorf("CoreDNS has %d replicas after restore", replicas(t, dyn))
	}
	if len(timer.cancelled) != 1 {
		t.Errorf("the crashed run's timer was not cancelled: %v", timer.cancelled)
	}
}

// A failed inject is put back at once, and the run is recorded as such.
func TestInjectFailure(t *testing.T) {
	m, dyn, runner, _ := testSetup(t)
	inject := runner.scripts["inject"]
	runner.scripts["inject"] = func() (string, error) {
		_, _ = inject()
		return "half done\n", exitErr(t)
	}
	if _, err := m.Start(context.Background(), "dns", false, io.Discard); err == nil || !strings.Contains(err.Error(), "everything is back") {
		t.Fatalf("start: %v", err)
	}
	st, _ := m.Store.Load()
	if replicas(t, dyn) != 2 || st.ActiveDrill != nil || st.Drills[0].Outcome != state.DrillInjectFailed {
		t.Errorf("replicas %d, state %+v", replicas(t, dyn), st)
	}
}

// The time limit: before the deadline Expire does nothing; after it, it
// restores; a timer for an older run does nothing.
func TestExpire(t *testing.T) {
	ctx := context.Background()
	m, dyn, _, c := testSetup(t)
	run, err := m.Start(ctx, "dns", false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	c.advance(29 * time.Minute)
	if done, err := m.Expire(ctx, run.Run, io.Discard); done || err != nil {
		t.Fatalf("before the deadline: %v, %v", done, err)
	}
	c.advance(50 * time.Second) // 10s early, as a timer with AccuracySec may fire
	if done, err := m.Expire(ctx, "20200101T000000Z", io.Discard); done || err != nil {
		t.Fatalf("another run's timer: %v, %v", done, err)
	}
	if done, err := m.Expire(ctx, "20200101T000000Z", io.Discard); done || err != nil {
		t.Fatalf("another run's timer: %v, %v", done, err)
	}
	if done, err := m.Expire(ctx, run.Run, io.Discard); !done || err != nil {
		t.Fatalf("after the deadline: %v, %v", done, err)
	}
	st, _ := m.Store.Load()
	if replicas(t, dyn) != 2 || st.Drills[0].Outcome != state.DrillExpired {
		t.Errorf("replicas %d, history %+v", replicas(t, dyn), st.Drills)
	}
}

// A restore that leaves an object changed keeps the run active, with the
// error, so abort can try again; nothing is lost.
func TestRestoreThatDoesNotStick(t *testing.T) {
	ctx := context.Background()
	m, dyn, runner, _ := testSetup(t)
	if _, err := m.Start(ctx, "dns", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	runner.scripts["restore"] = runner.scripts["inject"] // something scales it down again
	_, err := m.Abort(ctx, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "deployments/coredns: spec differ") {
		t.Fatalf("abort: %v", err)
	}
	st, _ := m.Store.Load()
	if st.ActiveDrill == nil || st.ActiveDrill.Phase != state.DrillRestoring || st.ActiveDrill.Error == "" {
		t.Fatalf("active = %+v", st.ActiveDrill)
	}
	runner.scripts["restore"] = nil
	if _, err := m.Abort(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	if replicas(t, dyn) != 2 {
		t.Error("not restored")
	}
}

// Drills refuse while a lab is started, a random drill hides its ID and
// picks only drills whose stacks are up.
func TestStartChecks(t *testing.T) {
	ctx := context.Background()
	m, _, _, _ := testSetup(t)
	m.Drills = append(m.Drills, Drill{ID: "needs-secrets", Stacks: []string{"secrets"}})
	if _, err := m.Start(ctx, "needs-secrets", false, io.Discard); err == nil || !strings.Contains(err.Error(), "lab up --stacks secrets") {
		t.Errorf("missing stack: %v", err)
	}
	if err := m.Store.Update(func(s *state.State) error { s.ActiveLab = "02-image-promotion"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(ctx, "dns", false, io.Discard); err == nil || !strings.Contains(err.Error(), "lab labs stop") {
		t.Errorf("with a lab started: %v", err)
	}
	if err := m.Store.Update(func(s *state.State) error { s.ActiveLab = ""; return nil }); err != nil {
		t.Fatal(err)
	}
	m.Pick = func(n int) int {
		if n != 1 {
			t.Errorf("random pool has %d drills, want only dns", n)
		}
		return 0
	}
	r, err := m.Start(ctx, "", true, io.Discard)
	if err != nil || r.Drill != "dns" || !r.Random || label(r) != "a random drill" {
		t.Fatalf("random: %+v, %v", r, err)
	}
	if _, err := m.Start(ctx, "dns", false, io.Discard); err == nil || strings.Contains(err.Error(), "dns") {
		t.Errorf("busy message gives the random drill away: %v", err)
	}
}

func TestEarliestAlert(t *testing.T) {
	since := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	b := []byte(`[{"startsAt":"2026-11-04T09:00:00Z"},{"startsAt":"2026-11-04T10:05:00Z"},{"startsAt":"2026-11-04T10:03:00Z"}]`)
	at, ok, err := earliestAlert(b, since)
	if err != nil || !ok || !at.Equal(since.Add(3*time.Minute)) {
		t.Errorf("earliestAlert = %v, %v, %v", at, ok, err)
	}
	if _, ok, _ := earliestAlert([]byte(`[{"startsAt":"2026-11-04T09:00:00Z"}]`), since); ok {
		t.Error("an alert firing before the drill counted")
	}
	// since = 1793790000. Series 1 starts firing at 1793790300; series 2
	// was firing before since (an earlier run's) and is not counted until
	// it stops and fires again at 1793790600.
	since = time.Unix(1793790000, 0)
	b = []byte(`{"status":"success","data":{"result":[
		{"values":[[1793790300,"1"],[1793790315,"1"]]},
		{"values":[[1793789985,"1"],[1793790000,"1"],[1793790015,"1"],[1793790600,"1"]]}]}}`)
	at, ok, err = firstSample(b, since)
	if err != nil || !ok || at.Unix() != 1793790300 {
		t.Errorf("firstSample = %v, %v, %v", at.Unix(), ok, err)
	}
	b = []byte(`{"status":"success","data":{"result":[{"values":[[1793789985,"1"],[1793790000,"1"],[1793790600,"1"]]}]}}`)
	if at, ok, _ := firstSample(b, since); !ok || at.Unix() != 1793790600 {
		t.Errorf("fired again after a gap: %v, %v", at.Unix(), ok)
	}
	b = []byte(`{"status":"success","data":{"result":[{"values":[[1793789985,"1"],[1793790000,"1"],[1793790015,"1"]]}]}}`)
	if _, ok, _ := firstSample(b, since); ok {
		t.Error("an alert firing since before the drill counted")
	}
	if _, ok, err := firstSample([]byte(`{"status":"success","data":{"result":[]}}`), since); ok || err != nil {
		t.Errorf("no samples: %v, %v", ok, err)
	}
}

func TestLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dns")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("drill.yaml", "id: dns\ntitle: DNS\nlevel: intermediate\nstacks: [sample-app]\ntimeLimit: 20m\n"+
		"alert: {name: CoreDNSDown}\nhints: [a]\ntouches: [kube-system]\n"+
		"snapshot:\n  - {apiVersion: apps/v1, resource: deployments, namespace: kube-system, name: coredns}\n")
	write("README.md", "# DNS\n\nThe pager says the app is down.\n\n## Debrief\n\nCoreDNS was at zero.\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "inject.sh") {
		t.Fatalf("missing scripts: %v", err)
	}
	for _, s := range scripts {
		write(s+".sh", "#!/bin/bash\n")
	}
	d, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Page != "The pager says the app is down." || d.Limit() != 20*time.Minute || d.Snapshot[0] != corednsRef {
		t.Errorf("drill = %+v", d)
	}
	write("drill.yaml", "id: dns\ntitle: DNS\nlevel: x\nstacks: [a]\nhints: [a]\ntouches: [b]\ntypo: 1\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "typo") {
		t.Errorf("unknown field: %v", err)
	}
}

// The drills in the repo load and are complete.
func TestRepoDrills(t *testing.T) {
	all, err := LoadAll("../../drills")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, d := range all {
		ids = append(ids, d.ID)
		if d.Alert.Name == "" {
			t.Errorf("%s names no alert", d.ID)
		}
	}
	if strings.Join(ids, " ") != "bad-deploy dns oom" {
		t.Errorf("drills = %v", ids)
	}
}
