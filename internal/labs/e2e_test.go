//go:build e2e

// The end-to-end tests run the labs for real: each step's verify script
// before and after its reference solution, a second verify (idempotent),
// then reset. They need Podman, kubectl, jq and network access.
//
// Lab 1 makes its own scratch kind cluster (and needs helm and argocd):
//
//	go test -tags e2e -run E2ELab1 -v -timeout 40m ./internal/labs/
//
// Labs on the main cluster (2, 3, 4 and 5) run only when
// LAB_E2E_MAIN_KUBECONFIG names a running lab's kubeconfig. They work only
// in their own lab0N-* namespaces and labelled cluster objects, and reset
// them after; they write the lab registry's mirror file next to it, as lab
// labs start does. Their stacks (registry, cluster, obs, sample-app,
// policy, secrets, sample-db) must be up; lab 4 reaches Moto on 127.0.0.1
// with the variables lab labs sets. One lab: -run E2EMain/04.
//
//	LAB_E2E_MAIN_KUBECONFIG=~/.lab/kubeconfig go test -tags e2e -run E2EMain -v -timeout 60m ./internal/labs/
package labs_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/labs"
	labaws "github.com/chanupen/devops-lab/internal/provider/aws"
	"github.com/chanupen/devops-lab/internal/provider/local"
	"github.com/chanupen/devops-lab/internal/state"
)

type logWriter struct{ t *testing.T }

func (w logWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func TestE2ELab1(t *testing.T) {
	for _, tool := range []string{"kubectl", "helm", "jq", "argocd", "podman"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("needs %s on PATH", tool)
		}
	}
	// The lab scripts get a throwaway HOME, so the argocd CLI session and
	// Helm's cache leave the user's own config alone. Only the scripts:
	// kind runs podman, and rootless Podman keeps its storage under HOME.
	tmpHome := t.TempDir()
	scriptEnv := []string{"HOME=" + tmpHome, "XDG_CONFIG_HOME=" + filepath.Join(tmpHome, ".config"),
		"XDG_CACHE_HOME=" + filepath.Join(tmpHome, ".cache"), "TMPDIR=" + tmpHome}

	all, err := labs.LoadAll(filepath.Join("..", "..", "labs"))
	if err != nil {
		t.Fatal(err)
	}
	labHome := t.TempDir()
	cfg, err := config.Load(filepath.Join("..", "..", "lab.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	scratch, err := local.NewScratch(labHome, cfg.K8sVersion)
	if err != nil {
		t.Fatal(err)
	}
	m := &labs.Manager{Labs: all, Store: state.NewStore(filepath.Join(labHome, "state.json")), Mode: "local", Clusters: scratch,
		Runner: labs.ExecRunner{}, Home: labHome, MainKubeconfig: filepath.Join(labHome, "kubeconfig"), LookPath: exec.LookPath,
		Env: scriptEnv,
		// Its own cluster name, so it never meets a learner's lab-scratch-<id>.
		ScratchName: func(l labs.Lab) string { return fmt.Sprintf("lab-scratch-e2e-%s-%d", l.Number(), os.Getpid()) }}
	ctx := context.Background()
	log := logWriter{t}

	var timings []string
	timed := func(what string, f func()) {
		start := time.Now()
		f()
		d := time.Since(start).Round(100 * time.Millisecond)
		timings = append(timings, fmt.Sprintf("%-38s %8s", what, d))
	}

	var l labs.Lab
	timed("start (create the scratch cluster)", func() {
		if l, err = m.Start(ctx, "01", log); err != nil {
			t.Fatal(err)
		}
	})
	t.Cleanup(func() {
		if b, err := os.ReadFile(filepath.Join(tmpHome, "lab-01-port-forward.pid")); err == nil {
			var pid int
			if _, err := fmt.Sscan(string(b), &pid); err == nil {
				_ = syscall.Kill(-pid, syscall.SIGTERM)
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
		start := time.Now()
		if _, _, err := m.Stop(context.Background(), log); err != nil {
			// Once, a podman --version run by kind exited 1 here; a second
			// try keeps a one-off from leaving the cluster behind.
			t.Logf("stop failed, trying once more: %v", err)
			time.Sleep(5 * time.Second)
			if _, _, err := m.Stop(context.Background(), log); err != nil {
				t.Errorf("stop: %v", err)
			}
		}
		t.Logf("%-38s %8s", "stop (delete the scratch cluster)", time.Since(start).Round(100*time.Millisecond))
		if ok, _ := scratch.Exists(m.Scratch(l)); ok {
			t.Errorf("cluster %s still exists after stop", m.Scratch(l))
		}
		t.Logf("timings:\n%s", strings.Join(timings, "\n"))
	})
	kubeconfig := m.Kubeconfig(l)

	runSteps(t, m, l, kubeconfig, scriptEnv, timed, 0)
	timed("reset", func() {
		if _, err := m.Reset(ctx, io.Discard); err != nil {
			t.Fatal(err)
		}
	})
	if _, res, _ := m.Verify(ctx, 1); res.Passed {
		t.Errorf("step 1 still passes after reset")
	} else {
		t.Logf("after reset, step 1: ✕ %s", res.Reason)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// runSteps runs each step of l: verify fails, the solution runs, verify
// passes (retried for a while: controllers need a moment), and passes
// again. staleFor is how long a "before" verify may still pass on data a
// previous run left behind (a metric's rate window); 0 means it must fail
// at once.
func runSteps(t *testing.T, m *labs.Manager, l labs.Lab, kubeconfig string, scriptEnv []string,
	timed func(string, func()), staleFor time.Duration) {
	t.Helper()
	ctx := context.Background()
	solve := func(n int) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		env := append([]string{"KUBECONFIG=" + kubeconfig, "LAB_ID=" + l.ID, "LAB_DIR=" + l.Dir, fmt.Sprintf("LAB_STEP=%d", n)}, scriptEnv...)
		out, err := labs.ExecRunner{}.Run(ctx, l.SolveScript(n), l.Dir, env)
		if err != nil {
			// One retry, logged: the solutions download charts and images,
			// and a network blip should not fail the lab.
			t.Logf("solution %d failed once, retrying: %v\n%s", n, err, out)
			if out, err = (labs.ExecRunner{}).Run(ctx, l.SolveScript(n), l.Dir, env); err != nil {
				t.Fatalf("solution %d: %v\n%s", n, err, out)
			}
		}
	}
	verifyUntil := func(n int, pass bool, within time.Duration) labs.Result {
		deadline := time.Now().Add(within)
		for {
			_, res, err := m.Verify(ctx, n)
			if err != nil {
				t.Fatal(err)
			}
			if res.Passed == pass || time.Now().After(deadline) {
				return res
			}
			time.Sleep(3 * time.Second)
		}
	}
	for _, s := range l.Steps {
		before := verifyUntil(s.N, false, staleFor)
		if before.Passed {
			t.Fatalf("step %d passes before its solution ran:\n%s", s.N, before.Output)
		}
		t.Logf("step %d before: ✕ %s", s.N, before.Reason)
		timed(fmt.Sprintf("step %d solution", s.N), func() { solve(s.N) })
		var res labs.Result
		timed(fmt.Sprintf("step %d verify (until it passes)", s.N), func() { res = verifyUntil(s.N, true, 3*time.Minute) })
		if !res.Passed {
			t.Fatalf("step %d: verify fails after the solution: %s\n%s", s.N, res.Reason, res.Output)
		}
		// Idempotent: a second run passes too and changes nothing.
		if _, again, _ := m.Verify(ctx, s.N); !again.Passed {
			t.Fatalf("step %d: second verify fails: %s", s.N, again.Reason)
		}
		t.Logf("step %d after: ● %s (%s)", s.N, strings.TrimSpace(lastLine(res.Output)), res.Duration.Round(100*time.Millisecond))
	}
	st, _ := m.Store.Load()
	if p := st.Labs[l.ID]; labs.LabMark(l, p) != labs.Done {
		t.Errorf("lab not done: %+v", p)
	}
}

// TestE2EMain runs the labs that use the main lab's cluster, one subtest
// each, against the kubeconfig in LAB_E2E_MAIN_KUBECONFIG.
func TestE2EMain(t *testing.T) {
	kubeconfig := os.Getenv("LAB_E2E_MAIN_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set LAB_E2E_MAIN_KUBECONFIG to a running lab's kubeconfig")
	}
	for _, tool := range []string{"kubectl", "jq", "curl", "podman"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("needs %s on PATH", tool)
		}
	}
	all, err := labs.LoadAll(filepath.Join("..", "..", "labs"))
	if err != nil {
		t.Fatal(err)
	}
	type labCase struct {
		id string
		// staleFor: lab 3's first steps read rates over 2 minutes, which
		// still hold a previous run's traffic for a while.
		staleFor time.Duration
	}
	tests := []labCase{{"02", 0}, {"03", 4 * time.Minute}, {"04", 0}, {"05", 0}}
	// LAB_E2E_MODE=cloud runs them on the cloud lab (kubeconfig
	// ~/.lab/cloud/kubeconfig), with lab 6, which runs only there.
	mode := os.Getenv("LAB_E2E_MODE")
	if mode == "" {
		mode = "local"
	}
	// The labs' environment: Zot and Moto locally; in cloud mode ECR, the
	// cloud session's keys and the lab tags (the same as lab labs env).
	modeEnv := append(local.LabRegistryEnv(), local.MotoEnv()...)
	prepare := func(labs.Lab) error { return local.EnsureLabRegistry(filepath.Dir(kubeconfig)) }
	if mode == "cloud" {
		tests = append(tests, labCase{"06", 0})
		if modeEnv, err = cloudLabsEnv(filepath.Dir(kubeconfig)); err != nil {
			t.Fatal(err)
		}
		prepare = nil
	}
	// LAB_E2E_LABS=02,06 runs only those.
	if only := os.Getenv("LAB_E2E_LABS"); only != "" {
		tests = slices.DeleteFunc(tests, func(c labCase) bool { return !slices.Contains(strings.Split(only, ","), c.id) })
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			labHome := t.TempDir()
			work := t.TempDir()
			// The lab's own state: its stacks are up on the main lab.
			store := state.NewStore(filepath.Join(labHome, "state.json"))
			if err := store.Update(func(s *state.State) error {
				for _, st := range []string{"registry", "cluster", "gitops", "core", "obs", "sample-app", "policy", "secrets", "sample-db"} {
					s.Stacks[st] = state.StackState{Status: state.Applied}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			scriptEnv := append(slices.Clone(modeEnv), "LAB_WORK="+work)
			m := &labs.Manager{Labs: all, Store: store, Mode: mode, Runner: labs.ExecRunner{}, Home: labHome,
				MainKubeconfig: kubeconfig, LookPath: exec.LookPath, Env: scriptEnv, Prepare: prepare}
			ctx := context.Background()
			var timings []string
			timed := func(what string, f func()) {
				start := time.Now()
				f()
				timings = append(timings, fmt.Sprintf("%-38s %8s", what, time.Since(start).Round(100*time.Millisecond)))
			}
			l, err := m.Start(ctx, tt.id, logWriter{t})
			if err != nil {
				t.Fatal(err)
			}
			total := time.Now()
			t.Cleanup(func() {
				start := time.Now()
				if _, err := m.Reset(context.Background(), logWriter{t}); err != nil {
					t.Errorf("reset: %v", err)
				}
				timings = append(timings, fmt.Sprintf("%-38s %8s", "reset", time.Since(start).Round(100*time.Millisecond)))
				if _, _, err := m.Stop(context.Background(), logWriter{t}); err != nil {
					t.Errorf("stop: %v", err)
				}
				t.Logf("timings:\n%s\n%-38s %8s", strings.Join(timings, "\n"), "total (steps and reset)", time.Since(total).Round(time.Second))
			})
			runSteps(t, m, l, m.Kubeconfig(l), scriptEnv, timed, tt.staleFor)
		})
	}
}

// cloudLabsEnv is lab labs env --mode cloud: home is the cloud lab's home
// (~/.lab/cloud), which holds its expires-at. The AWS session must be
// cached (lab cloud login); the test never asks for an MFA code.
func cloudLabsEnv(home string) ([]string, error) {
	cfg, err := config.Load(filepath.Join("..", "..", "lab.yaml"))
	if err != nil {
		return nil, err
	}
	cache, err := labaws.DefaultCacheDir()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p, err := labaws.New(ctx, labaws.Session{Profile: labaws.DefaultProfile, CacheDir: cache},
		labaws.Options{Home: home, Owner: cfg.Cloud.Owner, Profile: labaws.DefaultProfile})
	if err != nil {
		return nil, err
	}
	return p.LabsEnv()
}
