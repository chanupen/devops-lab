//go:build e2e

// The end-to-end test runs lab 1 for real: a scratch kind cluster on
// rootless Podman, each step's verify script before and after its
// reference solution, then reset and stop. It needs Podman, kubectl, helm,
// jq and the argocd CLI on PATH, and network access. It takes several
// minutes:
//
//	go test -tags e2e -run E2E -v -timeout 40m ./internal/labs/
package labs_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/labs"
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
	// The argocd CLI session and Helm's cache go to a throwaway HOME, so
	// the test leaves the user's own config alone.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpHome, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmpHome, ".cache"))
	t.Setenv("TMPDIR", tmpHome)

	all, err := labs.LoadAll(filepath.Join("..", "..", "labs"))
	if err != nil {
		t.Fatal(err)
	}
	labHome := t.TempDir()
	scratch, err := local.NewScratch(labHome)
	if err != nil {
		t.Fatal(err)
	}
	m := &labs.Manager{Labs: all, Store: state.NewStore(filepath.Join(labHome, "state.json")), Mode: "local", Clusters: scratch,
		Runner: labs.ExecRunner{}, Home: labHome, MainKubeconfig: filepath.Join(labHome, "kubeconfig"), LookPath: exec.LookPath}
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
		if _, err := m.Stop(context.Background(), log); err != nil {
			t.Errorf("stop: %v", err)
		}
		t.Logf("%-38s %8s", "stop (delete the scratch cluster)", time.Since(start).Round(100*time.Millisecond))
		if ok, _ := scratch.Exists(labs.ClusterName(l)); ok {
			t.Errorf("cluster %s still exists after stop", labs.ClusterName(l))
		}
		t.Logf("timings:\n%s", strings.Join(timings, "\n"))
	})
	kubeconfig := m.Kubeconfig(l)

	solve := func(n int) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		env := []string{"KUBECONFIG=" + kubeconfig, "LAB_ID=" + l.ID, "LAB_DIR=" + l.Dir, fmt.Sprintf("LAB_STEP=%d", n)}
		out, err := labs.ExecRunner{}.Run(ctx, l.SolveScript(n), l.Dir, env)
		if err != nil {
			t.Fatalf("solution %d: %v\n%s", n, err, out)
		}
	}
	// verifyUntil retries a few times: Argo CD needs a moment to report.
	verifyUntil := func(n int, within time.Duration) labs.Result {
		deadline := time.Now().Add(within)
		for {
			_, res, err := m.Verify(ctx, n)
			if err != nil {
				t.Fatal(err)
			}
			if res.Passed || time.Now().After(deadline) {
				return res
			}
			time.Sleep(3 * time.Second)
		}
	}

	for _, s := range l.Steps {
		_, before, err := m.Verify(ctx, s.N)
		if err != nil {
			t.Fatal(err)
		}
		if before.Passed {
			t.Fatalf("step %d passes before its solution ran:\n%s", s.N, before.Output)
		}
		t.Logf("step %d before: ✕ %s", s.N, before.Reason)
		timed(fmt.Sprintf("step %d solution", s.N), func() { solve(s.N) })
		var res labs.Result
		timed(fmt.Sprintf("step %d verify (until it passes)", s.N), func() { res = verifyUntil(s.N, 2*time.Minute) })
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
