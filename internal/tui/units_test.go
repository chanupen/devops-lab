package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chanupen/devops-lab/internal/labs"
)

// After a reboot Home says the cluster and the cache are stopped, with
// the command that starts them, rather than off.
func TestHomeUnits(t *testing.T) {
	down := cluster{known: true, reason: "not reachable: connection refused"}
	tests := []struct {
		name   string
		units  []Unit
		known  bool
		c      cluster
		want   []string
		absent []string
	}{
		{"units unknown: off, as before", nil, false, down,
			[]string{"cluster    ○ off (not reachable: connection refused)"}, []string{"stopped", "cache"}},
		{"cluster stopped", []Unit{{Kind: UnitCluster, Name: "lab", Fix: "podman start lab-control-plane lab-worker"}}, true, down,
			[]string{"health: ○ stopped", "cluster    ○ stopped $ podman start lab-control-plane lab-worker"},
			[]string{"health: ○ off", "cluster    ○ off"}},
		{"cluster running but not answering: still off", []Unit{{Kind: UnitCluster, Name: "lab", Running: true}}, true, down,
			[]string{"cluster    ○ off (not reachable"}, nil},
		{"cache stopped", []Unit{{Kind: UnitCluster, Name: "lab", Running: true}, {Kind: UnitCache, Name: "lab-zot", Fix: "podman start lab-zot"}},
			true, upCluster, []string{"cache      ○ stopped $ podman start lab-zot"}, nil},
		{"cache running", []Unit{{Kind: UnitCache, Name: "lab-zot", Running: true}}, true, upCluster,
			[]string{"cache      ● lab-zot"}, []string{"stopped"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(labState(1))
			m.d.live, m.d.units, m.d.unitsKnown = tt.c, tt.units, tt.known
			next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
			view := plain(next.(Model))
			for _, w := range tt.want {
				if !strings.Contains(view, w) {
					t.Errorf("view lacks %q:\n%s", w, view)
				}
			}
			for _, a := range tt.absent {
				if strings.Contains(view, a) {
					t.Errorf("view has %q:\n%s", a, view)
				}
			}
		})
	}
}

// The Labs screen counts a started lab's time only while its cluster runs
// and says when the cluster is stopped.
func TestLabsUnits(t *testing.T) {
	st := started(2)
	p := st.Labs["01-argocd-bootstrap"]
	p.Session = t0.Add(-15 * time.Hour) // started last night
	st.Labs["01-argocd-bootstrap"] = p
	name := "lab-scratch-01-argocd-bootstrap"
	tests := []struct {
		name  string
		units []Unit
		known bool
		want  string
	}{
		{"node down since 40 minutes in", []Unit{{Kind: UnitScratch, Name: name, Fix: "podman start " + name + "-control-plane",
			Uptime: labs.Uptime{Exists: true, Started: p.Session, Stopped: p.Session.Add(40 * time.Minute)}}}, true,
			"◐ started · cluster ○ stopped · 40m spent · $ podman start " + name + "-control-plane"},
		{"node started again 10 minutes ago", []Unit{{Kind: UnitScratch, Name: name, Running: true,
			Uptime: labs.Uptime{Exists: true, Running: true, Started: t0.Add(-10 * time.Minute)}}}, true, "◐ started · 10m spent"},
		{"cluster gone", nil, true, "◐ started · 0m spent"},
		{"not known yet: the whole session", nil, false, "◐ started · 15h00m spent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := labsModel1(t, st)
			m.d.units, m.d.unitsKnown = tt.units, tt.known
			m = pressLabs(t, m, "3")
			next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
			if view := plain(next.(Model)); !strings.Contains(view, tt.want) {
				t.Errorf("view lacks %q:\n%s", tt.want, view)
			}
		})
	}
}

// Units are listed at start, every few polls and when the cluster goes
// down or comes back; a failed list forgets what was known.
func TestUnitsRefresh(t *testing.T) {
	calls := 0
	m := newModel(labState(1))
	m.opts.Units = func(context.Context) ([]Unit, error) {
		calls++
		return []Unit{{Kind: UnitCache, Name: "lab-zot", Running: true}}, nil
	}
	run := func(m Model, msg tea.Msg) (Model, []tea.Msg) {
		next, cmd := m.Update(msg)
		return next.(Model), drain(cmd)
	}
	if msgs := drain(m.checkUnits()); len(msgs) != 1 {
		t.Fatalf("checkUnits: %v", msgs)
	} else {
		m, _ = run(m, msgs[0])
	}
	if !m.d.unitsKnown || len(m.d.units) != 1 || calls != 1 {
		t.Fatalf("after the first list: %+v, %d calls", m.d.units, calls)
	}
	for i := 1; i < unitsEvery; i++ {
		m, _ = run(m, tickMsg{})
	}
	if calls != 1 {
		t.Errorf("listed on every poll: %d calls", calls)
	}
	m, _ = run(m, tickMsg{})
	if calls != 2 {
		t.Errorf("not listed after %d polls: %d calls", unitsEvery, calls)
	}
	m.d.live = upCluster
	m, _ = run(m, liveMsg{gen: m.gen, c: cluster{known: true, reason: "refused"}})
	if calls != 3 {
		t.Errorf("not listed when the cluster went down: %d calls", calls)
	}
	m, _ = run(m, unitsMsg{err: errors.New("podman down")})
	if m.d.unitsKnown {
		t.Error("a failed list kept the old units")
	}
}

// drain runs cmd and any batch inside it, skipping commands that block
// (the watcher listener, ticks); only messages from quick commands return.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(100 * time.Millisecond):
		return nil
	}
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, drain(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}
