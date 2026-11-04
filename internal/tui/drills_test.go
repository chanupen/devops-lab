package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chanupen/devops-lab/internal/drills"
	"github.com/chanupen/devops-lab/internal/state"
)

// fakeDrills records which drill actions ran.
type fakeDrills struct {
	calls []string
}

func (f *fakeDrills) actions() DrillActions {
	return DrillActions{
		Start: func(_ context.Context, id string, random bool) (state.DrillRun, error) {
			f.calls = append(f.calls, "start "+id+map[bool]string{true: " random"}[random])
			return state.DrillRun{Drill: id}, nil
		},
		Verify: func(context.Context) (drills.Result, error) {
			f.calls = append(f.calls, "verify")
			return drills.Result{Reason: "CoreDNS has 0 replicas"}, nil
		},
		Ack: func(_ context.Context, cause string) (state.DrillRun, bool, error) {
			f.calls = append(f.calls, "ack "+cause)
			if cause != "coredns-down" {
				return state.DrillRun{Started: t0}, false, nil
			}
			return state.DrillRun{Started: t0, Acked: t0.Add(time.Minute)}, true, nil
		},
		AckCauses: func() []Cause {
			return []Cause{{"coredns-down", "CoreDNS is down"}, {"netpol", "A NetworkPolicy"}}
		},
		Hint: func() (int, int, string, error) {
			f.calls = append(f.calls, "hint")
			return 1, 4, "look at kube-system", nil
		},
		Abort: func(context.Context) (state.DrillRun, error) {
			f.calls = append(f.calls, "abort")
			return state.DrillRun{Drill: "dns", Outcome: state.DrillAborted}, nil
		},
		Expire: func(context.Context) (bool, error) { f.calls = append(f.calls, "expire"); return true, nil },
	}
}

// pressDrill sends keys and runs the commands they return, feeding back drill
// results (not state reloads, which read a file).
func pressDrill(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	var next tea.Model = m
	for _, k := range keys {
		var cmd tea.Cmd
		next, cmd = next.Update(key(k))
		if cmd != nil {
			if msg, ok := cmd().(drillMsg); ok {
				next, _ = next.Update(msg)
			}
		}
	}
	return next.(Model)
}

func TestDrillKeys(t *testing.T) {
	f := &fakeDrills{}
	st := labState(2)
	m := newModel(st)
	m.d.drills = []drills.Drill{{ID: "bad-deploy"}, {ID: "dns"}}
	m.opts.Drills = f.actions()

	m = pressDrill(t, m, "4", "j", "enter")
	if m.confirm == nil || m.confirm.command() != "lab drills start dns" {
		t.Fatalf("enter asks first: %+v", m.confirm)
	}
	m = pressDrill(t, m, "n")
	if len(f.calls) != 0 || !strings.Contains(m.flash, "cancelled: lab drills start dns") {
		t.Fatalf("n cancels: %v %q", f.calls, m.flash)
	}
	m = pressDrill(t, m, "enter", "y")
	if strings.Join(f.calls, ",") != "start dns" {
		t.Fatalf("calls = %v", f.calls)
	}
	m = pressDrill(t, m, "a")
	if !strings.Contains(m.flash, "no drill is running") {
		t.Errorf("ack without a drill: %q", m.flash)
	}

	m.d.st.ActiveDrill = &state.DrillRun{Drill: "dns", Phase: state.DrillRunning, Started: t0, Deadline: t0.Add(30 * time.Minute)}
	m.opts.Now = func() time.Time { return t0.Add(time.Minute) }
	m = pressDrill(t, m, "a")
	if m.drills.pick == nil || len(f.calls) != 1 {
		t.Fatalf("a opens the cause list: %+v %v", m.drills.pick, f.calls)
	}
	m = pressDrill(t, m, "2", "enter")
	if !strings.Contains(m.flash, "not the cause: netpol") {
		t.Errorf("wrong cause: %q", m.flash)
	}
	m = pressDrill(t, m, "a", "esc")
	if m.drills.pick != nil || len(f.calls) != 2 {
		t.Errorf("esc closes the list without an ack: %v", f.calls)
	}
	m = pressDrill(t, m, "a", "enter", "h", "v")
	if got := strings.Join(f.calls, ","); got != "start dns,ack netpol,ack coredns-down,hint,verify" {
		t.Fatalf("calls = %v", got)
	}
	if !strings.Contains(m.drills.last, "not recovered yet: CoreDNS has 0 replicas") || len(*m.drills.hints) != 1 {
		t.Errorf("last %q, hints %v", m.drills.last, *m.drills.hints)
	}
	m = pressDrill(t, m, "x", "y")
	if f.calls[len(f.calls)-1] != "abort" {
		t.Errorf("x y aborts: %v", f.calls)
	}

	// Read-only: no action runs.
	m.d.readOnly = "lab up (pid 42)"
	before := len(f.calls)
	m = pressDrill(t, m, "v")
	if len(f.calls) != before || !strings.Contains(m.flash, "read-only") {
		t.Errorf("read-only: calls %v, flash %q", f.calls[before:], m.flash)
	}
}

// Past the time limit, the TUI (which holds the lab lock, so the timer
// cannot) restores the drill on its next file poll.
func TestDrillExpiresInTUI(t *testing.T) {
	f := &fakeDrills{}
	st := labState(2)
	st.ActiveDrill = &state.DrillRun{Drill: "dns", Phase: state.DrillRunning, Started: t0, Deadline: t0.Add(30 * time.Minute)}
	m := newModel(st)
	m.opts.Drills = f.actions()
	m.opts.Now = func() time.Time { return t0.Add(29 * time.Minute) }
	m, cmd := m.drillTick()
	if cmd != nil {
		t.Fatal("expired before the deadline")
	}
	m.opts.Now = func() time.Time { return t0.Add(31 * time.Minute) }
	if !strings.Contains(drillHeader(&m.d, m.now()), "expired") {
		t.Errorf("header = %q", drillHeader(&m.d, m.now()))
	}
	m, cmd = m.drillTick()
	if cmd == nil {
		t.Fatal("no restore after the deadline")
	}
	next, _ := m.Update(cmd())
	if strings.Join(f.calls, ",") != "expire" || !strings.Contains(next.(Model).flash, "time limit passed") {
		t.Errorf("calls %v, flash %q", f.calls, next.(Model).flash)
	}
}
