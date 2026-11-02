package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/state"
)

func labsModel1(t *testing.T, st state.State) Model {
	t.Helper()
	all, err := labs.LoadAll(filepath.Join("..", "..", "labs"))
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(st)
	m.d.labs = all
	m.opts.Now = func() time.Time { return t0 }
	return m
}

// pressLabs sends keys and runs the command a key returned, as Bubble Tea would.
func pressLabs(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	var next tea.Model = m
	for _, k := range keys {
		var cmd tea.Cmd
		next, cmd = next.Update(key(k))
		if cmd != nil {
			if msg := cmd(); msg != nil {
				switch msg.(type) {
				case labVerifyMsg, labHintMsg:
					next, _ = next.Update(msg)
				}
			}
		}
	}
	return next.(Model)
}

func started(step int) state.State {
	st := labState(2)
	st.ActiveLab = "01-argocd-bootstrap"
	st.Labs = map[string]state.LabProgress{"01-argocd-bootstrap": {Step: step, Started: t0}}
	return st
}

func TestLabsKeys(t *testing.T) {
	var verified, hinted []int
	m := labsModel1(t, started(3))
	m.opts.LabVerify = func(_ context.Context, step int) (labs.Result, error) {
		verified = append(verified, step)
		return labs.Result{Step: step, Reason: "podinfo is OutOfSync, not Synced: argocd app sync podinfo"}, nil
	}
	m.opts.LabHint = func(step int) (int, string, error) {
		hinted = append(hinted, step)
		return step, "Hint 1 of 3: …", nil
	}

	// v on the list verifies the current step, and says the CLI command.
	m = pressLabs(t, m, "3", "v")
	if len(verified) != 1 || verified[0] != 3 || !strings.Contains(m.flash, "$ lab labs verify 3: podinfo is OutOfSync") {
		t.Errorf("v: verified %v, flash %q", verified, m.flash)
	}
	// enter opens step 3; n and p move; v and h act on the step shown.
	m = pressLabs(t, m, "enter", "n", "n", "p", "h")
	if m.labs.step != 4 || len(hinted) != 1 || hinted[0] != 4 || m.flash != "$ lab labs hint 4" {
		t.Errorf("step %d, hinted %v, flash %q", m.labs.step, hinted, m.flash)
	}
	m = pressLabs(t, m, "n", "n", "n") // stops at the last step
	if m.labs.step != 6 {
		t.Errorf("step %d after n past the end", m.labs.step)
	}
	if m = pressLabs(t, m, "esc"); m.labs.open {
		t.Error("esc did not close the step")
	}
}

func TestLabsKeysNeedAStartedLab(t *testing.T) {
	called := false
	m := labsModel1(t, labState(2))
	m.opts.LabVerify = func(context.Context, int) (labs.Result, error) { called = true; return labs.Result{}, nil }
	m = pressLabs(t, m, "3", "v")
	if called || m.flash != "lab 01 is not started: $ lab labs start 01" {
		t.Errorf("called %v, flash %q", called, m.flash)
	}
	// Without the functions (no labs/ next to lab.yaml) the keys point at the CLI.
	m = labsModel1(t, started(1))
	if m = pressLabs(t, m, "3", "h"); m.flash != "run it in a shell: $ lab labs hint 1" {
		t.Errorf("flash %q", m.flash)
	}
}

// One check at a time: a second v while one runs says busy.
func TestLabsOneCheckAtATime(t *testing.T) {
	m := labsModel1(t, started(2))
	m.opts.LabVerify = func(context.Context, int) (labs.Result, error) { return labs.Result{Passed: true}, nil }
	next, _ := m.Update(key("3"))
	next, cmd := next.Update(key("v"))
	if cmd == nil || !next.(Model).labs.busy {
		t.Fatal("v did not start a check")
	}
	next, _ = next.Update(key("v"))
	if f := next.(Model).flash; f != "busy: one lab check at a time" {
		t.Errorf("flash %q", f)
	}
}
