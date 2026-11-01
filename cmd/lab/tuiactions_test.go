package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/state"
	"github.com/chanupen/devops-lab/internal/tui"
)

func newTUIActions(t *testing.T, fp *fakeProvider, stateJSON string) tuiActions {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "state.json")
	if stateJSON != "" {
		if err := os.WriteFile(statePath, []byte(stateJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.ExtraStacks = nil
	return tuiActions{cfg: cfg, store: state.NewStore(statePath),
		newProvider: func(config.Config, string, runOptions) (provider, error) { return fp, nil }}
}

// collect gathers progress the way the TUI receives it.
type collect struct {
	mu     sync.Mutex
	lines  []string
	phases []string
}

func (c *collect) add(p tui.Progress) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p.Event != nil {
		c.phases = append(c.phases, p.Event.Step.Stack+" "+string(p.Event.Phase))
		return
	}
	c.lines = append(c.lines, p.Line)
}

func TestTUIActionsRun(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		req       tui.Request
		wantCalls []string
		phases    []string
		lines     []string // substrings, each in some line
		wantOp    string
	}{
		{name: "up of one stack", req: tui.Request{Stacks: []string{"cluster"}},
			wantCalls: []string{"apply registry", "apply cluster"},
			phases:    []string{"registry started", "registry finished", "cluster started", "cluster finished"},
			lines:     []string{"+ registry", "2 to apply", "$ lab up --stacks cluster", "lab up --stacks cluster: apply cluster"},
			wantOp:    "lab up --stacks cluster"},
		{name: "whole profile skips what the provider cannot run", req: tui.Request{},
			wantCalls: []string{"apply registry", "apply cluster"},
			lines:     []string{"- gitops      skip: not supported by the local provider yet", "3 skipped", "$ lab up"},
			wantOp:    "lab up"},
		{name: "down of one stack", state: deployedClusterRegistry, req: tui.Request{Down: true, Stacks: []string{"cluster"}},
			wantCalls: []string{"destroy cluster"}, lines: []string{"- cluster", "$ lab down --stacks cluster"},
			wantOp: "lab down --stacks cluster"},
		{name: "nothing to do", state: deployedClusterRegistry, req: tui.Request{Stacks: []string{"cluster"}},
			lines: []string{"= cluster", "nothing to do"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := &fakeProvider{}
			a := newTUIActions(t, fp, tt.state)
			var c collect
			if _, err := a.Run(context.Background(), tt.req, c.add); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(fp.calls, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", fp.calls, tt.wantCalls)
			}
			if tt.phases != nil && !slices.Equal(c.phases, tt.phases) {
				t.Errorf("phases = %v, want %v", c.phases, tt.phases)
			}
			all := strings.Join(c.lines, "\n")
			for _, w := range tt.lines {
				if !strings.Contains(all, w) {
					t.Errorf("output lacks %q:\n%s", w, all)
				}
			}
			st, err := a.store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantOp != "" && (len(st.Ops) == 0 || st.Ops[len(st.Ops)-1].Command != tt.wantOp) {
				t.Errorf("ops = %+v, want last command %q", st.Ops, tt.wantOp)
			}
		})
	}
}

// The TUI and the CLI claim the same lock: while lab up runs in another
// shell, the TUI's request is refused with ErrBusy, and nothing runs.
func TestTUIActionsBusy(t *testing.T) {
	fp := &fakeProvider{}
	a := newTUIActions(t, fp, "")
	unlock, err := a.store.LockOp("lab up")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	_, err = a.Run(context.Background(), tui.Request{}, func(tui.Progress) {})
	if !errors.Is(err, state.ErrBusy) || len(fp.calls) > 0 {
		t.Fatalf("err = %v, calls = %v; want busy and no calls", err, fp.calls)
	}
}

// The TUI records the same command the CLI would.
func TestTUICommandMatchesCLI(t *testing.T) {
	for _, req := range []tui.Request{{}, {Stacks: []string{"obs"}}, {Down: true, Stacks: []string{"sample-app"}}} {
		if got, want := command(req), req.Command(); got != want {
			t.Errorf("CLI %q, TUI %q", got, want)
		}
	}
}

func TestTUIActionsReport(t *testing.T) {
	a := newTUIActions(t, &fakeProvider{}, deployedClusterRegistry)
	cfgPath := filepath.Join(t.TempDir(), "lab.yaml")
	if err := os.WriteFile(cfgPath, []byte("profile: minimal\nextraStacks: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfgPath = cfgPath
	out, err := a.Report(context.Background(), "plan")
	if err != nil || !strings.Contains(out, "local mode, minimal profile") || !strings.Contains(out, "= cluster") {
		t.Errorf("plan report = %v\n%s", err, out)
	}
}

func TestPlanForPrompt(t *testing.T) {
	a := newTUIActions(t, &fakeProvider{}, deployedClusterRegistry)
	p, err := a.Plan(context.Background(), tui.Request{Down: true, Stacks: []string{"registry"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Count(engine.Destroy) != 2 || p.Steps[0].Stack != "cluster" {
		t.Errorf("plan = %+v, want cluster then registry destroyed", p.Steps)
	}
}

func TestBranchExists(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	work := filepath.Join(dir, "work")
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "--bare", remote)
	git("init", "-q", "-b", "main", work)
	git("-C", work, "commit", "-q", "--allow-empty", "-m", "init")
	git("-C", work, "push", "-q", remote, "main", "main:feat/live")

	exists := branchExists(config.Repo{URL: remote})
	for branch, want := range map[string]bool{"main": true, "feat/live": true, "feat/phase1-obs": false} {
		got, err := exists(context.Background(), branch)
		if err != nil || got != want {
			t.Errorf("%s: got %v, %v; want %v", branch, got, err, want)
		}
	}
	if _, err := branchExists(config.Repo{})(context.Background(), "main"); err == nil {
		t.Error("no repo URL: want an error, not an answer")
	}
	if _, err := branchExists(config.Repo{URL: filepath.Join(dir, "missing.git")})(context.Background(), "main"); err == nil {
		t.Error("unreachable remote: want an error, so no warning is shown")
	}
}

func TestConfigPath(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(work)
	labHome := filepath.Join(home, ".lab", "lab.yaml")
	write := func(p string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("mode: local\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(f labFlags, want string) {
		t.Helper()
		got, err := f.configPath()
		if err != nil || got != want {
			t.Errorf("configPath = %q, %v; want %q", got, err, want)
		}
	}
	check(labFlags{}, "")
	write(labHome)
	check(labFlags{}, labHome)
	write(filepath.Join(work, "lab.yaml"))
	check(labFlags{}, "lab.yaml")
	check(labFlags{cfgPath: "/elsewhere/lab.yaml"}, "/elsewhere/lab.yaml")
}
