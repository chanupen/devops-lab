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
// It needs a running lab with the obs and sample-app stacks, its
// kubeconfig in LAB_E2E_MAIN_KUBECONFIG, and podman, kubectl, jq and
// argocd. Its runs go to a throwaway state file, not the learner's
// history. One drill: -run E2EDrills/dns.
//
//	LAB_E2E_MAIN_KUBECONFIG=~/.lab/kubeconfig go test -tags e2e -run E2EDrills -v -timeout 90m ./internal/drills/
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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestE2EDrills(t *testing.T) {
	kubeconfig := os.Getenv("LAB_E2E_MAIN_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set LAB_E2E_MAIN_KUBECONFIG to a running lab's kubeconfig")
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
	// A throwaway state with the stacks the drills need marked applied.
	store := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err := store.Update(func(s *state.State) error {
		for _, st := range []string{"obs", "sample-app"} {
			s.Stacks[st] = state.StackState{Status: state.Applied}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir() // the scripts' HOME: argocd and podman caches stay out of the user's
	m := &drills.Manager{Drills: all, Store: store, Kubeconfig: kubeconfig, Runner: labs.ExecRunner{},
		Kube:   func() (dynamic.Interface, error) { return dyn, nil },
		Alerts: drills.ProxyAlerts{REST: core.CoreV1().RESTClient()},
		Env:    append(local.LabRegistryEnv(), "XDG_CONFIG_HOME="+filepath.Join(tmp, ".config"), "TMPDIR="+tmp)}
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
		})
	}
	_ = log
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
