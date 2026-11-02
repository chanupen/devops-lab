package labs

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/state"
)

// fakeRunner answers by script name: an output and whether it passes.
type fakeRunner struct {
	out   map[string]string
	pass  map[string]bool
	block bool // wait for the context instead (a hung script)
	runs  []string
	envs  [][]string
}

func (f *fakeRunner) Run(ctx context.Context, script, _ string, env []string) (string, error) {
	name := filepath.Base(script)
	f.runs, f.envs = append(f.runs, name), append(f.envs, env)
	if f.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if f.pass[name] {
		return f.out[name], nil
	}
	// A real non-zero exit, so Verify sees an *exec.ExitError.
	return f.out[name], exec.Command("false").Run()
}

type fakeClusters struct {
	have    map[string]bool
	created []string
	deleted []string
}

func (f *fakeClusters) Exists(name string) (bool, error) { return f.have[name], nil }
func (f *fakeClusters) Create(_ context.Context, name, _ string, _ io.Writer) error {
	f.created = append(f.created, name)
	f.have[name] = true
	return nil
}
func (f *fakeClusters) Delete(_ context.Context, name, _ string, _ io.Writer) error {
	f.deleted = append(f.deleted, name)
	delete(f.have, name)
	return nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func testManager(t *testing.T) (*Manager, *fakeRunner, *fakeClusters, *clock) {
	t.Helper()
	root := t.TempDir()
	scratch, err := Load(writeLab(t, root, "01-scratch", 3, NeedsScratch))
	if err != nil {
		t.Fatal(err)
	}
	onLab, err := Load(writeLab(t, root, "02-on-lab", 1, NeedsLab))
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{out: map[string]string{}, pass: map[string]bool{}}
	c := &fakeClusters{have: map[string]bool{}}
	clk := &clock{t: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)}
	home := t.TempDir()
	m := &Manager{Labs: []Lab{scratch, onLab}, Store: state.NewStore(filepath.Join(home, "state.json")), Mode: "local",
		Clusters: c, Runner: r, Home: home, MainKubeconfig: filepath.Join(home, "kubeconfig"),
		LookPath: func(string) (string, error) { return "/bin/x", nil }, Now: clk.now}
	return m, r, c, clk
}

func progress(t *testing.T, m *Manager, id string) state.LabProgress {
	t.Helper()
	st, err := m.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return st.Labs[id]
}

func TestLabFlow(t *testing.T) {
	ctx := context.Background()
	m, r, c, clk := testManager(t)

	l, err := m.Start(ctx, "1", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if l.ID != "01-scratch" || !slices.Equal(c.created, []string{"lab-scratch-01-scratch"}) {
		t.Errorf("start: %s, created %v", l.ID, c.created)
	}
	if p := progress(t, m, l.ID); p.Step != 1 || !p.Started.Equal(clk.t) {
		t.Errorf("progress after start: %+v", p)
	}
	if _, err := m.Start(ctx, "02-on-lab", io.Discard); err == nil || !strings.Contains(err.Error(), "lab labs stop first") {
		t.Errorf("second lab: %v", err)
	}

	// A failing verify: the reason is the script's last line; no progress.
	r.out["01-verify.sh"] = "looking...\nnot Ready yet: argocd-server-0\n"
	_, res, err := m.Verify(ctx, 0)
	if err != nil || res.Passed || res.Reason != "not Ready yet: argocd-server-0" || res.Step != 1 {
		t.Fatalf("failing verify: %+v %v", res, err)
	}
	env := strings.Join(r.envs[0], " ")
	if !strings.Contains(env, "KUBECONFIG="+filepath.Join(m.Home, "labs", "01-scratch", "kubeconfig")) || !strings.Contains(env, "LAB_STEP=1") {
		t.Errorf("env: %s", env)
	}
	// Passing moves the lab on and counts attempts.
	r.pass["01-verify.sh"] = true
	if _, res, _ := m.Verify(ctx, 0); !res.Passed {
		t.Fatalf("passing verify: %+v", res)
	}
	if p := progress(t, m, l.ID); p.Step != 2 || p.Attempts[1] != 2 {
		t.Errorf("after step 1: %+v", p)
	}
	// Verifying an earlier step again does not move the lab back or on.
	if _, _, err := m.Verify(ctx, 1); err != nil || progress(t, m, l.ID).Step != 2 {
		t.Errorf("re-verify step 1: %v %+v", err, progress(t, m, l.ID))
	}
	if _, _, err := m.Verify(ctx, 9); err == nil {
		t.Error("step 9 of 3 accepted")
	}

	// Hints come one at a time, then a pointer to the solution.
	var hints []string
	for range 3 {
		_, n, h, err := m.Hint(0)
		if err != nil || n != 2 {
			t.Fatalf("hint: %d %v", n, err)
		}
		hints = append(hints, h)
	}
	if hints[0] != "Hint 1 of 2: h1" || hints[1] != "Hint 2 of 2: h2" || !strings.Contains(hints[2], "lab labs solution 2") {
		t.Errorf("hints = %q", hints)
	}

	_, sol, err := m.Solution("", 2)
	if err != nil || !strings.Contains(sol, "echo solve 2") || !slices.Equal(progress(t, m, l.ID).Solutions, []int{2}) {
		t.Errorf("solution: %q %v %v", sol, err, progress(t, m, l.ID).Solutions)
	}

	// Stop keeps progress, adds the session's time and deletes the cluster.
	clk.t = clk.t.Add(25 * time.Minute)
	if _, err := m.Stop(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	p := progress(t, m, l.ID)
	if p.Step != 2 || p.Spent != 25*time.Minute || !p.Session.IsZero() || !slices.Equal(c.deleted, []string{"lab-scratch-01-scratch"}) {
		t.Errorf("after stop: %+v deleted %v", p, c.deleted)
	}
	if _, _, err := m.Verify(ctx, 0); err == nil || !strings.Contains(err.Error(), "no lab is started") {
		t.Errorf("verify while stopped: %v", err)
	}

	// Starting again picks up at step 2; the time adds up across sessions.
	if _, err := m.Start(ctx, "01-scratch", io.Discard); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(10 * time.Minute)
	if p := progress(t, m, l.ID); p.Step != 2 || p.TimeSpent(clk.t) != 35*time.Minute {
		t.Errorf("second session: %+v, spent %s", p, p.TimeSpent(clk.t))
	}
	r.pass["02-verify.sh"], r.pass["03-verify.sh"] = true, true
	for range 2 {
		if _, res, err := m.Verify(ctx, 0); err != nil || !res.Passed {
			t.Fatalf("verify: %+v %v", res, err)
		}
	}
	p = progress(t, m, l.ID)
	if p.Step != 4 || !p.Completed.Equal(clk.t) || LabMark(l, p) != Done || DoneSteps(l, p) != 3 {
		t.Errorf("done: %+v", p)
	}

	// Reset runs reset.sh and starts the steps over, keeping the time.
	r.pass["reset.sh"] = true
	if _, err := m.Reset(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	p = progress(t, m, l.ID)
	if p.Step != 1 || !p.Completed.IsZero() || p.Hints != nil || p.TimeSpent(clk.t) != 35*time.Minute {
		t.Errorf("after reset: %+v", p)
	}
	if !slices.Contains(r.runs, "reset.sh") {
		t.Errorf("reset.sh not run: %v", r.runs)
	}
}

func TestVerifyTimeout(t *testing.T) {
	m, r, _, _ := testManager(t)
	m.Timeout = 100 * time.Millisecond
	if _, err := m.Start(context.Background(), "1", io.Discard); err != nil {
		t.Fatal(err)
	}
	r.block = true
	start := time.Now()
	_, res, err := m.Verify(context.Background(), 0)
	if err != nil || res.Passed || res.Reason != "timed out after 100ms" || time.Since(start) < m.Timeout {
		t.Errorf("timeout: %+v %v after %s", res, err, time.Since(start))
	}
}

func TestStartChecks(t *testing.T) {
	ctx := context.Background()
	m, _, c, _ := testManager(t)
	m.LookPath = func(f string) (string, error) {
		if f == "argocd" {
			return "", errors.New("not found")
		}
		return "/bin/" + f, nil
	}
	m.Labs[0].Tools = []string{"kubectl", "argocd", "jq"}
	m.Labs[0].Install = map[string]map[string]string{"argocd": {"linux/amd64": "curl -fsSLo /tmp/argocd https://example.invalid/v3.5.5/argocd-linux-amd64"}}
	m.Platform = "linux/amd64"
	m.LookPath = func(f string) (string, error) {
		if f == "argocd" || f == "jq" {
			return "", errors.New("not found")
		}
		return "/bin/" + f, nil
	}
	_, err := m.Start(ctx, "1", io.Discard)
	if err == nil || len(c.created) != 0 {
		t.Fatalf("missing tools: %v, created %v", err, c.created)
	}
	// The exact pinned command for this platform, not a pointer to the README.
	want := "lab 01-scratch needs argocd, jq on PATH\n  install argocd:\n    curl -fsSLo /tmp/argocd https://example.invalid/v3.5.5/argocd-linux-amd64\n" +
		"  jq: install it with your package manager"
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("missing tools:\n%s\nwant prefix:\n%s", err, want)
	}
	m.Mode = "cloud"
	if _, err := m.Start(ctx, "1", io.Discard); err == nil || !strings.Contains(err.Error(), "runs in local mode") {
		t.Errorf("wrong mode: %v", err)
	}
	m.Mode = "local"
	// needs: lab wants the main lab's stacks, and uses its kubeconfig.
	if _, err := m.Start(ctx, "2", io.Discard); err == nil || !strings.Contains(err.Error(), "lab up --stacks core") {
		t.Errorf("main lab down: %v", err)
	}
	if err := m.Store.Update(func(s *state.State) error {
		s.Stacks["core"] = state.StackState{Status: state.Applied}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	l, err := m.Start(ctx, "2", io.Discard)
	if err != nil || len(c.created) != 0 || m.Kubeconfig(l) != m.MainKubeconfig {
		t.Errorf("on the main lab: %v, created %v, kubeconfig %s", err, c.created, m.Kubeconfig(l))
	}
	if _, err := m.Stop(ctx, io.Discard); err != nil || len(c.deleted) != 0 {
		t.Errorf("stopping a needs: lab lab deleted %v (%v)", c.deleted, err)
	}
}

// The real runner stops a script and what it started at the timeout.
func TestExecRunnerKillsTheProcessGroup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hang.sh")
	if err := os.WriteFile(script, []byte("#!/bin/bash\nsleep 30 &\necho started\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := ExecRunner{}.Run(ctx, script, dir, []string{"X=1"})
	if err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("err %v after %s", err, time.Since(start))
	}
}

func TestMarks(t *testing.T) {
	p := state.LabProgress{Step: 3}
	got := ""
	for n := 1; n <= 4; n++ {
		got += StepMark(p, n).Symbol()
	}
	if got != "●●◐○" {
		t.Errorf("marks = %s", got)
	}
	if StepMark(state.LabProgress{}, 1) != NotStarted {
		t.Error("an unstarted lab has a current step")
	}
}

// A scratch cluster with the lab's name that this state did not start is
// someone else's (another lab home, a test run): start refuses it, and
// creates and changes nothing. The lab's own cluster is reused on restart.
func TestStartRefusesAForeignCluster(t *testing.T) {
	ctx := context.Background()
	m, _, c, _ := testManager(t)
	c.have["lab-scratch-01-scratch"] = true
	_, err := m.Start(ctx, "1", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "a kind cluster named lab-scratch-01-scratch already exists, but "+m.Store.Path()+" did not start it") ||
		!strings.Contains(err.Error(), "kind delete cluster --name lab-scratch-01-scratch") {
		t.Fatalf("foreign cluster: %v", err)
	}
	if st, _ := m.Store.Load(); st.ActiveLab != "" || len(c.created) != 0 {
		t.Errorf("state %q, created %v", st.ActiveLab, c.created)
	}
	// Our own: started here, so starting again picks it up.
	delete(c.have, "lab-scratch-01-scratch")
	if _, err := m.Start(ctx, "1", io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(ctx, "1", io.Discard); err != nil {
		t.Errorf("restarting our own lab: %v", err)
	}
	// A test run names its clusters apart, so it never meets this one.
	m.ScratchName = func(l Lab) string { return "lab-scratch-e2e-" + l.Number() + "-42" }
	if l, _ := m.Find("1"); m.Scratch(l) != "lab-scratch-e2e-01-42" {
		t.Errorf("scratch name = %s", m.Scratch(l))
	}
}
