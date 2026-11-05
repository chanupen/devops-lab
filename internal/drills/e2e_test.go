//go:build e2e

// The end-to-end test runs each drill on the main lab for real, twice:
//
//  1. inject, verify fails, wait for the drill's alert to fire (its time
//     to alert is logged), run solution.sh, verify passes (lab puts the
//     lab back), and every object the drill may touch is as before;
//  2. inject, abort, and every object is as before.
//
// "As before" compares, before and after, the drill's saved objects, every
// Argo CD Application and the Deployments, StatefulSets, DaemonSets and
// Services in the namespaces the drill touches (content, labels and
// annotations; status and controller-owned fields left out). If anything
// fails the drill is aborted first, so no fault is left behind.
//
// Before recovery, verify's message must not name the cause: none of the
// drill's spoilers may appear in it. After every run no kind node may be
// stopped or NotReady and no netem rule may be left on Postgres.
//
// It needs a running lab with the obs, sample-app, secrets and sample-db
// stacks, its kubeconfig in LAB_E2E_MAIN_KUBECONFIG, and podman, kubectl,
// jq and argocd. It holds the lab lock while it runs, like lab drills
// start. Its runs are test runs in ~/.lab/e2e/state.json (kubeconfig
// linked beside it), not the learner's history: a run interrupted
// mid-drill stays there, lab doctor reports it and lab up restores it,
// and the next e2e run aborts it first. One drill: -run E2EDrills/dns.
//
//	LAB_E2E_MAIN_KUBECONFIG=~/.lab/kubeconfig go test -tags e2e -run E2EDrills -v -timeout 60m ./internal/drills/
//
// TestE2EKilledDrill kills a dns run mid-inject, then checks lab doctor
// fails on it and lab up puts the lab back (under a minute):
//
//	LAB_E2E_MAIN_KUBECONFIG=~/.lab/kubeconfig go test -tags e2e -run E2EKilledDrill -v -timeout 20m ./internal/drills/
package drills_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/chanupen/devops-lab/internal/drills"
	"github.com/chanupen/devops-lab/internal/kube"
	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/provider/local"
	"github.com/chanupen/devops-lab/internal/state"
)

type logWriter struct{ t *testing.T }

func (w logWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// fingerprinted are the kinds compared in a drill's namespaces.
var fingerprinted = []schema.GroupVersionResource{
	{Group: "apps", Version: "v1", Resource: "deployments"},
	{Group: "apps", Version: "v1", Resource: "statefulsets"},
	{Group: "apps", Version: "v1", Resource: "daemonsets"},
	{Version: "v1", Resource: "services"},
}

var appGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

// fingerprint is every compared object, normalized, by kind/namespace/name.
func fingerprint(ctx context.Context, t *testing.T, dyn dynamic.Interface, d drills.Drill) map[string]any {
	t.Helper()
	out := map[string]any{}
	add := func(g schema.GroupVersionResource, ns string) {
		list, err := dyn.Resource(g).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			t.Fatalf("list %s in %s: %v", g.Resource, ns, err)
		}
		for i := range list.Items {
			u := &list.Items[i]
			out[g.Resource+"/"+u.GetNamespace()+"/"+u.GetName()] = drills.Normalize(u)
		}
	}
	add(appGVR, "argocd")
	for _, ns := range d.Touches {
		for _, g := range fingerprinted {
			add(g, ns)
		}
	}
	return out
}

// differences names the objects whose fingerprints differ.
func differences(a, b map[string]any) []string {
	var out []string
	for k, v := range a {
		w, ok := b[k]
		switch {
		case !ok:
			out = append(out, k+": gone")
		case !reflect.DeepEqual(roundTrip(v), roundTrip(w)):
			x, _ := json.Marshal(v)
			y, _ := json.Marshal(w)
			out = append(out, fmt.Sprintf("%s: changed\n  before %s\n  after  %s", k, x, y))
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, k+": new")
		}
	}
	slices.Sort(out)
	return out
}

func roundTrip(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// backAsBefore waits until the fingerprint matches before: Argo CD and the
// Deployment controller take a little while to settle after a restore.
func backAsBefore(ctx context.Context, t *testing.T, dyn dynamic.Interface, d drills.Drill, before map[string]any) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for {
		diffs := differences(before, fingerprint(ctx, t, dyn, d))
		if len(diffs) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("not back as before the drill:\n%s", strings.Join(diffs, "\n"))
		}
		time.Sleep(10 * time.Second)
	}
}

// e2eLab is what both tests need: the drills, the main lab's clients and
// a manager on the e2e state file.
type e2eLab struct {
	all        []drills.Drill
	kubeconfig string
	dyn        dynamic.Interface
	core       kubernetes.Interface
	m          *drills.Manager
	main       string // the lab's own state file, ~/.lab/state.json
}

func setupE2E(t *testing.T) e2eLab {
	t.Helper()
	kubeconfig := os.Getenv("LAB_E2E_MAIN_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set LAB_E2E_MAIN_KUBECONFIG to a running lab's kubeconfig")
	}
	kubeconfig, err := filepath.Abs(kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"kubectl", "jq", "argocd", "podman"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("needs %s on PATH", tool)
		}
	}
	all, err := drills.LoadAll(filepath.Join("..", "..", "drills"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := kube.RESTConfig(kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	main, err := state.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	// The e2e state, with the stacks the drills need marked applied, and
	// the kubeconfig beside it, where lab finds it to restore a run.
	path := state.E2EPath(main)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(path), "kubeconfig")
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(kubeconfig, link); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(path)
	if err := store.Update(func(s *state.State) error {
		for _, st := range []string{"obs", "sample-app", "secrets", "sample-db"} {
			s.Stacks[st] = state.StackState{Status: state.Applied}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir() // the scripts' HOME: argocd and podman caches stay out of the user's
	m := &drills.Manager{Drills: all, Store: store, Kubeconfig: link, Runner: labs.ExecRunner{},
		Kube:   func() (dynamic.Interface, error) { return dyn, nil },
		Alerts: drills.ProxyAlerts{REST: core.CoreV1().RESTClient()},
		Test:   true,
		Env: append(append(local.LabRegistryEnv(), local.MotoEnv()...),
			"XDG_CONFIG_HOME="+filepath.Join(tmp, ".config"), "TMPDIR="+tmp)}
	return e2eLab{all: all, kubeconfig: kubeconfig, dyn: dyn, core: core, m: m, main: main}
}

// claim takes the lab lock for the test, so lab up and lab drills wait
// for it, and refuses to run beside a drill or a lab of the user's. A
// drill an interrupted e2e run left behind is aborted first.
func (e e2eLab) claim(ctx context.Context, t *testing.T) {
	t.Helper()
	main := state.NewStore(e.main)
	unlock, err := main.LockLab(fmt.Sprintf("go test %s (pid %d, since %s)", t.Name(), os.Getpid(), time.Now().Format("15:04")))
	if err != nil {
		t.Fatalf("lab lock: %v", err)
	}
	t.Cleanup(unlock)
	st, err := main.Load()
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case st.ActiveDrill != nil:
		t.Fatalf("drill %s is running in %s: lab drills abort first", st.ActiveDrill.Drill, e.main)
	case st.ActiveLab != "":
		t.Fatalf("lab %s is started: lab labs stop first", st.ActiveLab)
	}
	if r, _, ok, err := e.m.Active(); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Logf("an earlier e2e run left drill %s (%s) in %s: aborting it", r.Drill, r.Phase, e.m.Store.Path())
		if _, err := e.m.Abort(ctx, logWriter{t}); err != nil {
			t.Fatalf("abort the earlier run: %v", err)
		}
	}
}

func TestE2EDrills(t *testing.T) {
	e := setupE2E(t)
	all, kubeconfig, dyn, m := e.all, e.kubeconfig, e.dyn, e.m
	e.claim(context.Background(), t)
	ctx := context.Background()
	log := logWriter{t}

	for _, d := range all {
		t.Run(d.ID, func(t *testing.T) {
			log := logWriter{t}
			// Never leave a fault behind: whatever fails, restore first.
			t.Cleanup(func() {
				if _, _, ok, _ := m.Active(); ok {
					t.Log("cleanup: aborting the drill")
					if _, err := m.Abort(ctx, log); err != nil {
						t.Errorf("cleanup abort: %v", err)
					}
				}
			})
			before := fingerprint(ctx, t, dyn, d)

			// 1. inject, verify fails, the alert fires, solution, verify passes.
			run, err := m.Start(ctx, d.ID, false, log)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			res, err := m.Verify(ctx, io.Discard)
			if err != nil || res.Passed {
				t.Fatalf("verify right after inject: passed %v, %v", res.Passed, err)
			}
			t.Logf("verify before the fix: %s", res.Reason)
			for _, w := range d.Spoilers {
				if strings.Contains(strings.ToLower(res.Reason), strings.ToLower(w)) {
					t.Errorf("verify before the fix gives the cause away (%q): %s", w, res.Reason)
				}
			}
			fired := waitAlert(ctx, t, m, 12*time.Minute)
			if fired.IsZero() {
				t.Errorf("alert %s did not fire within 12 minutes", d.Alert.Name)
			} else {
				t.Logf("TIME TO ALERT %s: %s (%s)", d.ID, fired.Sub(run.Started).Round(time.Second), d.Alert.Name)
			}
			out, err := runScript(ctx, d, kubeconfig, m.Env, "solution")
			t.Log(out)
			if err != nil {
				t.Fatalf("solution.sh: %v", err)
			}
			res = verifyUntil(ctx, t, m, 3*time.Minute)
			r := res.Run
			t.Logf("TIMES %s: alert %s, recover %s (solution run right after the alert)", d.ID,
				r.TimeTo(r.AlertFired).Round(time.Second), r.TimeTo(r.Recovered).Round(time.Second))
			if r.Outcome != state.DrillRecovered {
				t.Fatalf("after a passing verify the run is %+v", r)
			}
			backAsBefore(ctx, t, dyn, d, before)
			nothingLeft(ctx, t, d, kubeconfig)

			// 2. inject, abort, back as before.
			if _, err := m.Start(ctx, d.ID, false, log); err != nil {
				t.Fatalf("second start: %v", err)
			}
			if res, err := m.Verify(ctx, io.Discard); err != nil || res.Passed {
				t.Fatalf("verify after the second inject: passed %v, %v", res.Passed, err)
			}
			if _, err := m.Abort(ctx, log); err != nil {
				t.Fatalf("abort: %v", err)
			}
			backAsBefore(ctx, t, dyn, d, before)
			nothingLeft(ctx, t, d, kubeconfig)
		})
	}
	_ = log
}

// leftovers fails when a kind node of the lab is stopped or NotReady, or
// a netem rule is on a Postgres container: faults node-loss and slow-db
// must never leave behind.
const leftovers = `. "$1/lib.sh"
bad=0
for n in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
  kind_node_running "$n" || { echo "kind node $n is not running"; bad=1; continue; }
  node_ready "$n" || { echo "node $n is not Ready"; bad=1; }
  if pod_netns "$n" sample-db postgres tc qdisc show dev eth0 2>/dev/null | grep -q netem; then
    echo "a netem rule is left on Postgres on $n"; bad=1
  fi
done
exit $bad`

func nothingLeft(ctx context.Context, t *testing.T, d drills.Drill, kubeconfig string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "bash", "-c", leftovers, "leftovers", filepath.Dir(d.Dir))
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("left behind after %s: %s", d.ID, out)
	}
}

// waitAlert checks the drill's alert until it fires or the time is up.
func waitAlert(ctx context.Context, t *testing.T, m *drills.Manager, d time.Duration) time.Time {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		r, err := m.CheckAlert(ctx)
		if err != nil {
			t.Logf("alert check: %v", err)
		}
		if !r.AlertFired.IsZero() {
			return r.AlertFired
		}
		time.Sleep(15 * time.Second)
	}
	return time.Time{}
}

// verifyUntil runs verify until it passes: the fix takes a rollout.
func verifyUntil(ctx context.Context, t *testing.T, m *drills.Manager, d time.Duration) drills.Result {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		res, err := m.Verify(ctx, logWriter{t})
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if res.Passed {
			return res
		}
		if time.Now().After(deadline) {
			t.Fatalf("verify still fails after the solution: %s", res.Reason)
		}
		t.Logf("not yet: %s", res.Reason)
		time.Sleep(10 * time.Second)
	}
}

func runScript(ctx context.Context, d drills.Drill, kubeconfig string, env []string, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return labs.ExecRunner{}.Run(ctx, d.Script(name), d.Dir, append([]string{"KUBECONFIG=" + kubeconfig, "DRILL_ID=" + d.ID, "DRILL_DIR=" + d.Dir}, env...))
}

// killedEnv marks the child process TestE2EKilledDrill starts.
const killedEnv = "LAB_E2E_KILLED_CHILD"

// TestE2EKilledDrill runs the dns drill's e2e in a child process and
// kills it (SIGKILL, with its scripts) while inject waits for CoreDNS to
// stop: the fault is in and the run's state says injecting. Then lab
// doctor must fail on the e2e drill, and lab up must put the lab back as
// it was before, with nothing left in ~/.lab/e2e/state.json.
func TestE2EKilledDrill(t *testing.T) {
	if os.Getenv(killedEnv) != "" {
		t.Skip("the child runs TestE2EDrills only")
	}
	e := setupE2E(t)
	ctx := context.Background()
	d, err := e.m.Find("dns")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := e.m.Active(); err != nil || ok {
		t.Fatalf("an e2e drill is already in the lab (%v): lab up first", err)
	}
	// Never leave a fault behind: if lab up did not restore it, abort.
	t.Cleanup(func() {
		if _, _, ok, _ := e.m.Active(); ok {
			t.Log("cleanup: aborting the drill")
			if _, err := e.m.Abort(ctx, logWriter{t}); err != nil {
				t.Errorf("cleanup abort: %v", err)
			}
		}
	})
	before := fingerprint(ctx, t, e.dyn, d)

	bin := filepath.Join(t.TempDir(), "lab")
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", bin, "../../cmd/lab").CombinedOutput(); err != nil {
		t.Fatalf("build lab: %v\n%s", err, out)
	}
	cfg, err := filepath.Abs(filepath.Join("..", "..", "lab.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	child := exec.CommandContext(ctx, os.Args[0], "-test.run", "^TestE2EDrills$/^dns$", "-test.v", "-test.timeout", "15m")
	child.Env = append(os.Environ(), killedEnv+"=1")
	child.Stdout, child.Stderr = logWriter{t}, logWriter{t}
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	coredns := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	midInject := func() bool {
		st, err := e.m.Store.Load()
		if err != nil || st.ActiveDrill == nil {
			return false
		}
		if st.ActiveDrill.Phase != state.DrillInjecting {
			t.Fatalf("the run reached %s before lab could kill it mid-inject", st.ActiveDrill.Phase)
		}
		u, err := e.dyn.Resource(coredns).Namespace("kube-system").Get(ctx, "coredns", metav1.GetOptions{})
		if err != nil {
			return false
		}
		n, found, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
		return found && n == 0
	}
	deadline := time.Now().Add(3 * time.Minute)
	for !midInject() {
		select {
		case err := <-exited:
			t.Fatalf("the e2e child ended before the kill: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
			t.Fatal("the dns inject did not start within 3 minutes")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := syscall.Kill(-child.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill the e2e child: %v", err)
	}
	if err := <-exited; err == nil {
		t.Fatal("the e2e child exited cleanly; want it killed")
	}
	t.Log("killed the e2e child mid-inject: CoreDNS is at zero")
	if r, _, ok, err := e.m.Active(); err != nil || !ok || r.Phase != state.DrillInjecting {
		t.Fatalf("after the kill the e2e state has %+v (active %v, %v); want the run, injecting", r, ok, err)
	}

	// lab doctor fails on it.
	out, _ := exec.CommandContext(ctx, bin, "doctor", "--json").Output()
	var report struct {
		Results []struct{ Name, Status, Detail, Fix string }
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("lab doctor --json: %v\n%s", err, out)
	}
	i := slices.IndexFunc(report.Results, func(r struct{ Name, Status, Detail, Fix string }) bool { return r.Name == "drill" })
	if i < 0 {
		t.Fatalf("lab doctor has no drill check:\n%s", out)
	}
	if r := report.Results[i]; r.Status != "fail" || !strings.Contains(r.Detail, "e2e test drill dns") || !strings.Contains(r.Fix, "lab up") {
		t.Fatalf("lab doctor drill check: %+v; want fail on the e2e test drill dns, fix lab up", r)
	} else {
		t.Logf("lab doctor: %s %s (fix: %s)", r.Status, r.Detail, r.Fix)
	}

	// lab up restores it before anything else. It keeps the revision the
	// lab runs (a branch under test), so it changes nothing else.
	args := []string{"up", "--config", cfg}
	if st, err := state.NewStore(e.main).Load(); err != nil {
		t.Fatal(err)
	} else if st.Revision != "" && st.Revision != "main" {
		args = append(args, "--revision", st.Revision)
	}
	up := exec.CommandContext(ctx, bin, args...)
	upOut, err := up.CombinedOutput()
	t.Logf("lab up:\n%s", upOut)
	if err != nil {
		t.Fatalf("lab up: %v", err)
	}
	for _, want := range []string{"an e2e test run's drill is still in the lab (injecting)", "● the drill is restored"} {
		if !strings.Contains(string(upOut), want) {
			t.Errorf("lab up does not say %q", want)
		}
	}
	if r, _, ok, err := e.m.Active(); err != nil || ok {
		t.Fatalf("after lab up the e2e state still has %+v (%v)", r, err)
	}
	backAsBefore(ctx, t, e.dyn, d, before)
	nothingLeft(ctx, t, d, e.kubeconfig)
}
