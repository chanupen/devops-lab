package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/state"
)

// fakeActions is a fake engine: Run reports a step and a log line, then
// waits for release, so a test can act while an operation is running.
type fakeActions struct {
	mu      sync.Mutex
	runs    []string
	plan    engine.Plan
	runErr  error
	release chan struct{}
	reports map[string]string
}

func (f *fakeActions) Plan(context.Context, Request) (engine.Plan, error) { return f.plan, nil }

func (f *fakeActions) Run(ctx context.Context, req Request, progress func(Progress)) (engine.Plan, error) {
	f.mu.Lock()
	f.runs = append(f.runs, req.Command())
	f.mu.Unlock()
	step := engine.Step{Stack: "sample-app", Action: engine.Apply}
	if req.Down {
		step.Action = engine.Destroy
	}
	progress(Progress{Event: &engine.Event{Step: step, Phase: engine.Started, Log: "/tmp/x.log"}})
	progress(Progress{Line: "12:00:00.000 helm upgrade sample-app"})
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return engine.Plan{}, ctx.Err()
		}
	}
	return engine.Plan{Steps: []engine.Step{step}}, f.runErr
}

func (f *fakeActions) Report(_ context.Context, name string) (string, error) {
	return f.reports[name], nil
}

func modelWith(acts Actions, st state.State) Model {
	m := New(Options{Config: newModel(st).opts.Config, State: st, Catalog: engine.Catalog(), Actions: acts,
		Now: func() time.Time { return t0.Add(10 * time.Minute) }})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return next.(Model)
}

// press sends keys and returns the model and the last command.
func press(m Model, keys ...string) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.Update(key(k))
		m = next.(Model)
	}
	return m, cmd
}

// drive runs cmd and feeds every message it produces back into the model
// until stop says so. Batches run concurrently, like Bubble Tea runs them.
func drive(t *testing.T, m Model, cmd tea.Cmd, stop func(tea.Msg) bool) Model {
	t.Helper()
	msgs := make(chan tea.Msg, 64)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, c := range b {
					run(c)
				}
				return
			}
			if msg != nil {
				msgs <- msg
			}
		}()
	}
	run(cmd)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-msgs:
			next, c := m.Update(msg)
			m = next.(Model)
			if stop(msg) {
				return m
			}
			if _, ok := msg.(tickMsg); !ok {
				run(c)
			}
		case <-deadline:
			t.Fatal("timed out driving the model")
		}
	}
}

func strip(m Model) string { return ansi.Strip(m.opsStrip()) }

// A second action while one runs is refused with "busy", whatever the key,
// and the first finishes normally.
func TestOneOperationAtATime(t *testing.T) {
	acts := &fakeActions{release: make(chan struct{})}
	m := modelWith(acts, labState(1))
	m, cmd := press(m, "U")
	if m.op == nil || cmd == nil {
		t.Fatal("U did not start an operation")
	}
	// Let it report its first step before trying more keys.
	m = drive(t, m, cmd, func(msg tea.Msg) bool { _, ok := msg.(opProgressMsg); return ok })
	if s := strip(m); !strings.Contains(s, "◐ $ lab up") || !strings.Contains(s, "sample-app: applying") || !strings.Contains(s, "helm upgrade sample-app") {
		t.Errorf("ops strip while running = %q", s)
	}
	for _, keys := range [][]string{{"U"}, {"2", "u"}, {"2", "d"}, {":", "u", "p", "enter"}, {":", "d", "o", "w", "n", "enter"}} {
		next, _ := press(m, keys...)
		if next.op != m.op {
			t.Errorf("%v: replaced the running operation", keys)
		}
		if s := strip(next); !strings.Contains(s, "busy: lab up is running") || !strings.Contains(s, "$ lab up") {
			t.Errorf("%v: ops strip = %q, want busy and the running op", keys, s)
		}
	}
	close(acts.release)
	m = drive(t, m, m.listenOp(), func(msg tea.Msg) bool { _, ok := msg.(opDoneMsg); return ok })
	if m.op != nil || !strings.Contains(m.flash, "lab up: done in") {
		t.Errorf("after the run: op %v, flash %q", m.op, m.flash)
	}
	if !slices.Equal(acts.runs, []string{"lab up"}) {
		t.Errorf("runs = %v, want one lab up", acts.runs)
	}
}

// Another process holding the lab is reported when Run returns.
func TestBusyElsewhere(t *testing.T) {
	acts := &fakeActions{runErr: fmt.Errorf("plan: %w", &state.BusyError{Holder: "lab up (pid 42)"})}
	m := modelWith(acts, labState(1))
	m, cmd := press(m, "2", "u")
	m = drive(t, m, cmd, func(msg tea.Msg) bool { _, ok := msg.(opDoneMsg); return ok })
	if !strings.Contains(m.flash, "lab and its components are already running: lab up (pid 42); lab up --stacks registry did not run") {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestStackKeys(t *testing.T) {
	failed := labState(1)
	failed.Stacks["sample-app"] = state.StackState{Status: state.Failed, Error: "timeout"}
	downPlan := engine.Plan{Steps: []engine.Step{{Stack: "sample-app", Action: engine.Destroy}}}
	tests := []struct {
		name    string
		st      state.State
		keys    []string
		plan    engine.Plan
		wantReq string // command started; "" for none
		flash   string
		confirm string
	}{
		{name: "u on Home needs a selection", st: labState(1), keys: []string{"u"}, flash: "acts on the selected stack"},
		{name: "u brings the selected stack up", st: labState(1), keys: []string{"2", "G", "k", "u"}, wantReq: "lab up --stacks sample-app"},
		{name: "r on a healthy stack explains", st: labState(1), keys: []string{"2", "r"}, flash: "registry has not failed"},
		{name: "r retries a failed stack", st: failed, keys: []string{"2", "G", "k", "r"}, wantReq: "lab up --stacks sample-app"},
		{name: "d asks first", st: labState(1), keys: []string{"2", "G", "k", "d"}, plan: downPlan, confirm: "Destroy sample-app?"},
		{name: "d then n cancels", st: labState(1), keys: []string{"2", "G", "k", "d", "n"}, plan: downPlan, flash: "cancelled: lab down --stacks sample-app"},
		{name: "d then y tears down", st: labState(1), keys: []string{"2", "G", "k", "d", "y"}, plan: downPlan, wantReq: "lab down --stacks sample-app"},
		{name: "d on a stack that is not deployed", st: labState(1), keys: []string{"2", "/", "a", "i", "enter", "d"},
			flash: "nothing to destroy: ai is not deployed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acts := &fakeActions{plan: tt.plan}
			m := modelWith(acts, tt.st)
			for _, k := range tt.keys {
				var cmd tea.Cmd
				m, cmd = press(m, k)
				if k == "d" && cmd != nil { // the plan for the prompt runs in the background
					next, _ := m.Update(cmd())
					m = next.(Model)
				}
			}
			got := ""
			if m.op != nil {
				got = m.op.req.Command()
			}
			if got != tt.wantReq {
				t.Errorf("started %q, want %q (flash %q)", got, tt.wantReq, m.flash)
			}
			if tt.flash != "" && !strings.Contains(ansi.Strip(m.flash), tt.flash) {
				t.Errorf("flash = %q, want %q", m.flash, tt.flash)
			}
			if tt.confirm != "" && (m.confirm == nil || m.confirm.question != tt.confirm) {
				t.Errorf("confirm = %+v, want %q", m.confirm, tt.confirm)
			}
			if m.op != nil {
				m.op.cancel()
			}
		})
	}
}

func TestConfirmNamesDependents(t *testing.T) {
	m := modelWith(&fakeActions{}, labState(1))
	m = m.confirmFor(planMsg{req: Request{Down: true, Stacks: []string{"core"}}, plan: engine.Plan{Steps: []engine.Step{
		{Stack: "sample-app", Action: engine.Destroy}, {Stack: "obs", Action: engine.Destroy}, {Stack: "core", Action: engine.Destroy}}}})
	if want := "Destroy core and 2 stacks that need it (sample-app, obs)?"; m.confirm == nil || m.confirm.question != want {
		t.Errorf("question = %+v, want %q", m.confirm, want)
	}
}

func TestPalette(t *testing.T) {
	acts := &fakeActions{plan: engine.Plan{Steps: []engine.Step{{Stack: "sample-app", Action: engine.Destroy}}},
		reports: map[string]string{"doctor": "● podman rootless\n● kind 0.31"}}
	m := modelWith(acts, labState(1))

	m, _ = press(m, ":", "d", "o", "w", "n", "s", "a", "m")
	e, ok := m.palette.selected()
	if !ok || e.label != ":down sample-app" || e.cli != "lab down --stacks sample-app" {
		t.Fatalf("best match = %+v", e)
	}
	m, cmd := press(m, "enter")
	if m.palette != nil || cmd == nil {
		t.Fatal("enter did not close the palette and plan the teardown")
	}
	next, _ := m.Update(cmd())
	if c := next.(Model).confirm; c == nil || c.req.Command() != "lab down --stacks sample-app" {
		t.Errorf("confirm = %+v", c)
	}

	m, _ = press(modelWith(acts, labState(1)), ":", "d", "o", "c")
	m, cmd = press(m, "enter")
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.view == nil || m.view.title != "$ lab doctor" || !slices.Equal(m.view.lines.slice(0, 2), []string{"● podman rootless", "● kind 0.31"}) {
		t.Errorf("doctor view = %+v", m.view)
	}

	m, _ = press(modelWith(acts, labState(1)), ":", "u", "p")
	if m, _ = press(m, "esc"); m.palette != nil {
		t.Error("esc did not close the palette")
	}
}

func TestFuzzyScore(t *testing.T) {
	tests := []struct {
		s, q string
		ok   bool
	}{
		{"up obs", "", true}, {"up obs", "obs", true}, {"up obs", "uo", true}, {"down sample-app", "dsa", true},
		{"status", "sts", true}, {"up obs", "x", false}, {"plan", "nalp", false}, {"Doctor", "DOC", true},
	}
	for _, tt := range tests {
		if _, ok := fuzzyScore(tt.s, tt.q); ok != tt.ok {
			t.Errorf("fuzzyScore(%q, %q) ok = %v, want %v", tt.s, tt.q, ok, tt.ok)
		}
	}
	// A match at word starts beats one scattered through a word.
	a, _ := fuzzyScore("up obs", "obs")
	b, _ := fuzzyScore("down obs-like-dobs", "obs")
	if a <= b {
		t.Errorf("score(up obs) = %d, want more than %d", a, b)
	}
}

func TestK9s(t *testing.T) {
	dir := t.TempDir()
	kc := filepath.Join(dir, "kubeconfig")
	missing := func(string) (string, error) { return "", errors.New("not found") }
	found := func(string) (string, error) { return "/usr/local/bin/k9s", nil }
	tests := []struct {
		name     string
		lookPath func(string) (string, error)
		kube     bool
		flash    string
		cmd      bool
	}{
		{"k9s missing shows the install hint", missing, true, k9sInstallHint, false},
		{"no cluster", found, false, "K needs the lab cluster", false},
		{"hands off with the lab context and kubeconfig", found, true, "$ k9s --context kind-lab --kubeconfig " + kc, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Remove(kc)
			if tt.kube {
				if err := os.WriteFile(kc, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			m := New(Options{Config: newModel(labState(0)).opts.Config, State: labState(0), Catalog: engine.Catalog(),
				Kubeconfig: kc, LookPath: tt.lookPath})
			m.screen = screenStacks
			m, cmd := press(m, "K")
			if !strings.Contains(m.flash, tt.flash) || (cmd != nil) != tt.cmd {
				t.Errorf("flash %q cmd %v, want %q cmd %v", m.flash, cmd != nil, tt.flash, tt.cmd)
			}
			next, _ := m.Update(k9sDoneMsg{})
			if next.(Model).screen != screenStacks {
				t.Error("did not come back to the same screen")
			}
		})
	}
}

func TestDeletedBranch(t *testing.T) {
	asked := ""
	m := New(Options{Config: newModel(labState(0)).opts.Config, State: labState(1), Catalog: engine.Catalog(),
		BranchExists: func(_ context.Context, b string) (bool, error) { asked = b; return false, nil }})
	cmd := m.checkBranch()
	if cmd == nil {
		t.Fatal("no branch check for revision feat/x")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if asked != "feat/x" {
		t.Errorf("checked %q, want feat/x", asked)
	}
	view := plain(m)
	for _, want := range []string{"rev: feat/x (deleted)", "revision   feat/x (deleted)", "           " + deletedHint} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	if m.checkBranch() != nil {
		t.Error("checked the same revision twice")
	}

	onMain := labState(1)
	onMain.Revision = "main"
	if New(Options{State: onMain, BranchExists: func(context.Context, string) (bool, error) { return true, nil }}).checkBranch() != nil {
		t.Error("checked main")
	}
}

func TestOpsScreen(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "op.log")
	var lines []string
	for i := range logLines + 10 {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := labState(2)
	st.Ops[1].Log = logPath
	m := modelWith(&fakeActions{}, st)
	m, _ = press(m, "5")
	view := plain(m)
	if !strings.Contains(view, "Ops log  2 operations, newest first") || strings.Index(view, "02:26:00") > strings.Index(view, "02:25:00") {
		t.Errorf("ops screen is not newest first:\n%s", view)
	}
	m, cmd := press(m, "enter")
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.view == nil || m.view.title != "$ lab up" || m.view.lines.len() != logLines || m.view.lines.dropped() != 10 {
		t.Fatalf("view = %+v", m.view)
	}
	if got := m.view.lines.slice(logLines-1, logLines); got[0] != fmt.Sprintf("line %d", logLines+9) {
		t.Errorf("last line = %q", got)
	}
	if m, _ = press(m, "esc"); m.view != nil || m.screen != screenOps {
		t.Error("esc did not return to the ops list")
	}
}

func TestRing(t *testing.T) {
	r := newRing(3)
	r.add("a", "b")
	if got := r.slice(0, 5); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("slice = %v", got)
	}
	r.add("c", "d", "e")
	if got := r.slice(0, 3); !slices.Equal(got, []string{"c", "d", "e"}) || r.dropped() != 2 || r.len() != 3 {
		t.Errorf("slice = %v dropped %d len %d", got, r.dropped(), r.len())
	}
	if got := r.slice(1, 2); !slices.Equal(got, []string{"d"}) {
		t.Errorf("slice(1,2) = %v", got)
	}
}

// Progress is batched: a burst of lines reaches Update as few messages.
func TestProgressIsBatched(t *testing.T) {
	burst := &burstActions{n: 500}
	m := modelWith(burst, labState(0))
	m, cmd := press(m, "U")
	progressMsgs := 0
	m = drive(t, m, cmd, func(msg tea.Msg) bool {
		if _, ok := msg.(opProgressMsg); ok {
			progressMsgs++
		}
		_, done := msg.(opDoneMsg)
		return done
	})
	if progressMsgs > 5 {
		t.Errorf("%d progress messages for one burst, want a few", progressMsgs)
	}
	if got := m.flash; !strings.Contains(got, "done in") {
		t.Errorf("flash = %q", got)
	}
}

type burstActions struct{ n int }

func (b *burstActions) Plan(context.Context, Request) (engine.Plan, error) { return engine.Plan{}, nil }
func (b *burstActions) Report(context.Context, string) (string, error)     { return "", nil }
func (b *burstActions) Run(_ context.Context, _ Request, progress func(Progress)) (engine.Plan, error) {
	for i := range b.n {
		progress(Progress{Line: fmt.Sprintf("line %d", i)})
	}
	return engine.Plan{Steps: []engine.Step{{Stack: "core", Action: engine.Apply}}}, nil
}

// A TUI opened while another lab holds the lock shows views and reports but
// refuses every action, says why, and turns read-write once it can claim
// the lab.
func TestReadOnly(t *testing.T) {
	const holder = "lab up --stacks obs (pid 42 on /dev/pts/3, since 17:20)"
	acts := &fakeActions{plan: engine.Plan{Steps: []engine.Step{{Stack: "sample-app", Action: engine.Destroy}}},
		reports: map[string]string{"status": "local mode"}}
	claims, released := 0, false
	claim := func() (func(), error) {
		claims++
		if claims == 1 {
			return nil, &state.BusyError{Holder: holder}
		}
		return func() { released = true }, nil
	}
	m := New(Options{Config: newModel(labState(1)).opts.Config, State: labState(1), Catalog: engine.Catalog(), Actions: acts,
		ReadOnly: holder, Claim: claim, LookPath: func(string) (string, error) { return "", errors.New("none") }})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = next.(Model)

	if h := ansi.Strip(m.header()); !strings.Contains(h, "read-only: another lab is running (pid 42)") {
		t.Errorf("header = %q", h)
	}
	for _, keys := range [][]string{{"U"}, {"2", "u"}, {"2", "d"}, {"2", "r"}, {":", "u", "p", "enter"}, {":", "d", "o", "w", "n", "enter"}} {
		got, cmd := press(m, keys...)
		want := "read-only: another lab is running: " + holder + "; lab "
		if got.op != nil || got.confirm != nil || cmd != nil || !strings.Contains(ansi.Strip(got.flash), want) {
			if keys[len(keys)-1] == "r" && strings.Contains(got.flash, "has not failed") {
				continue // r explains first that the stack has not failed
			}
			t.Errorf("%v: op %v confirm %v cmd %v flash %q; want refused as read-only", keys, got.op != nil, got.confirm != nil, cmd != nil, got.flash)
		}
	}
	// Views and reports still work.
	if got, _ := press(m, "5"); got.screen != screenOps {
		t.Error("5 did not open the ops log")
	}
	got, cmd := press(m, ":", "s", "t", "a", "t", "enter")
	if cmd == nil {
		t.Fatal(":status did not run in read-only mode")
	}
	next, _ = got.Update(cmd())
	if v := next.(Model).view; v == nil || v.title != "$ lab status" {
		t.Errorf(":status view = %+v", v)
	}
	if got, _ := press(m, "K"); !strings.Contains(got.flash, k9sInstallHint) {
		t.Errorf("K in read-only mode: flash %q, want the k9s hint, not a refusal", got.flash)
	}

	// The file check retries the claim: still busy, then free.
	for i, want := range []string{holder, ""} {
		next, cmd := m.Update(tickMsg{})
		m = next.(Model)
		var claimed tea.Msg
		for _, c := range cmd().(tea.BatchMsg) {
			if c == nil {
				continue
			}
			if msg, ok := c().(claimMsg); ok {
				claimed = msg
			}
		}
		if claimed == nil {
			t.Fatalf("tick %d: no claim", i)
		}
		next, _ = m.Update(claimed)
		m = next.(Model)
		if m.d.readOnly != want {
			t.Errorf("tick %d: readOnly = %q, want %q", i, m.d.readOnly, want)
		}
	}
	if !strings.Contains(m.flash, "this TUI can act now") || strings.Contains(ansi.Strip(m.header()), "read-only") {
		t.Errorf("after the claim: flash %q header %q", m.flash, ansi.Strip(m.header()))
	}
	if got, _ := press(m, "U"); got.op == nil {
		t.Error("U did not start once the TUI holds the lab")
	} else {
		got.op.cancel()
	}
	m.lease.drop()
	if !released {
		t.Error("the lock taken later was not released on exit")
	}
	if m.claim() != nil {
		t.Error("a TUI that holds the lab still tries to claim it")
	}
}
