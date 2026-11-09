package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/drills"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/gitops"
	"github.com/chanupen/devops-lab/internal/kube"
	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/state"
)

var t0 = time.Date(2026, 10, 31, 2, 25, 0, 0, time.UTC)

// labState is a lab with core, obs and sample-app applied from a branch.
func labState(ops int) state.State {
	st := state.New()
	st.Mode, st.Revision = "local", "feat/x"
	for _, s := range []string{"registry", "cluster", "gitops", "core", "obs", "sample-app"} {
		st.Stacks[s] = state.StackState{Status: state.Applied, UpdatedAt: t0}
	}
	for i := range ops {
		st.AppendOp(state.Op{Command: "lab up", Stack: "core", Action: "apply", Status: "ok",
			Started: t0.Add(time.Duration(i) * time.Minute), Finished: t0.Add(time.Duration(i)*time.Minute + 41*time.Second)})
	}
	return st
}

func newModel(st state.State) Model {
	return New(Options{Config: config.Default(), State: st, Catalog: engine.Catalog(),
		AppURL: func(h string) string { return "https://" + h + ":8443" }, AccessHint: "on the Mac: ssh -N fedora-lab"})
}

func plain(m Model) string { return ansi.Strip(m.View()) }

// The first frame comes from state.json alone: load, build, render, with no
// cluster call, in under 100 ms.
func TestFirstFrame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := state.NewStore(path).Update(func(s *state.State) error { *s = labState(state.MaxOps); return nil }); err != nil {
		t.Fatal(err)
	}
	called := false
	start := time.Now()
	st, err := state.NewStore(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	m := New(Options{Config: config.Default(), State: st, StatePath: path, Kubeconfig: filepath.Join(filepath.Dir(path), "kubeconfig"),
		Catalog: engine.Catalog(),
		Labs:    []labs.Lab{{ID: "fixture", Modes: []string{"local"}}}, DrillList: []drills.Drill{{ID: "fixture"}},
		Units: func(context.Context) ([]Unit, error) {
			called = true
			time.Sleep(time.Second)
			return nil, fmt.Errorf("slow failed units")
		},
		Connect: func(string) (*kube.Clients, error) {
			called = true
			time.Sleep(time.Second)
			return nil, fmt.Errorf("slow failed connection")
		}})
	view := m.View()
	elapsed := time.Since(start)
	t.Logf("first frame in %s", elapsed)
	if elapsed > 100*time.Millisecond {
		t.Errorf("first frame took %s, want under 100ms", elapsed)
	}
	if called {
		t.Error("the first frame must not touch the cluster")
	}
	for _, want := range []string{"mode: local", "rev: feat/x", "1 Home", "2 Stacks", "Last operations", "lab up  core apply  41s", "progress", "labs 0/1 complete | drills 0/1 recovered"} {
		if !strings.Contains(ansi.Strip(view), want) {
			t.Errorf("first frame lacks %q:\n%s", want, ansi.Strip(view))
		}
	}
}

func TestViewFitsTheWindow(t *testing.T) {
	m := newModel(labState(3))
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 160, Height: 50}, {Width: 20, Height: 5}} {
		next, _ := m.Update(size)
		lines := strings.Split(next.(Model).View(), "\n")
		w, h := max(size.Width, 40), max(size.Height, 10)
		if len(lines) != h {
			t.Errorf("%dx%d: %d lines, want %d", size.Width, size.Height, len(lines), h)
		}
		for i, l := range lines {
			if got := ansi.StringWidth(l); got != w {
				t.Errorf("%dx%d: line %d is %d cells, want %d", size.Width, size.Height, i, got, w)
				break
			}
		}
	}
}

func TestLeftWidth(t *testing.T) {
	tests := []struct {
		name string
		left string
		w    int
		want int
	}{
		{"narrow content keeps the minimum", "short", 100, minLeft},
		{"sizes to the widest line plus a margin", "a\n" + strings.Repeat("x", 50) + "\nb", 100, 51},
		{"ANSI codes take no width", "\x1b[1m" + strings.Repeat("x", 50) + "\x1b[0m", 100, 51},
		{"wide symbols count as cells", strings.Repeat("●", 45), 100, 46},
		{"leaves room for the detail pane", strings.Repeat("x", 90), 100, 100 - 1 - minRight},
		{"a narrow window keeps the minimum", strings.Repeat("x", 90), 50, minLeft},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := leftWidth(tt.left, tt.w); got != tt.want {
				t.Errorf("leftWidth = %d, want %d", got, tt.want)
			}
		})
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestKeys(t *testing.T) {
	tests := []struct {
		name     string
		keys     []string
		screen   int
		selected string
		filter   string
		help     bool
		quit     bool
	}{
		{"starts on home", nil, screenHome, "registry", "", false, false},
		{"2 opens stacks", []string{"2"}, screenStacks, "registry", "", false, false},
		{"j and down move", []string{"2", "j", "down"}, screenStacks, "gitops", "", false, false},
		{"k stops at the top", []string{"2", "j", "k", "k", "up"}, screenStacks, "registry", "", false, false},
		{"G goes to the last", []string{"2", "G"}, screenStacks, "scaling", "", false, false},
		{"j stops at the bottom", []string{"2", "G", "j"}, screenStacks, "scaling", "", false, false},
		{"1 goes home", []string{"2", "1"}, screenHome, "registry", "", false, false},
		{"filter narrows the list", []string{"2", "/", "o", "b", "s", "enter"}, screenStacks, "obs", "obs", false, false},
		{"q while filtering is text", []string{"2", "/", "q"}, screenStacks, "", "q", false, false},
		{"esc clears the filter", []string{"2", "/", "o", "b", "s", "esc"}, screenStacks, "registry", "", false, false},
		{"j moves inside the filter result", []string{"2", "/", "c", "enter", "j"}, screenStacks, "core", "c", false, false},
		{"keys typed in one burst", []string{"2jj"}, screenStacks, "gitops", "", false, false},
		{"? opens help", []string{"?"}, screenHome, "registry", "", true, false},
		{"any key closes help", []string{"?", "j"}, screenHome, "registry", "", false, false},
		{"q quits", []string{"q"}, screenHome, "registry", "", false, true},
		{"q quits from help", []string{"?", "q"}, screenHome, "registry", "", true, true},
		{"ctrl+c quits while filtering", []string{"2", "/", "ctrl+c"}, screenStacks, "registry", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(labState(1))
			quit := false
			for _, k := range tt.keys {
				next, cmd := m.Update(key(k))
				m = next.(Model)
				if cmd != nil && (k == "q" || k == "ctrl+c") {
					if _, ok := cmd().(tea.QuitMsg); ok {
						quit = true
					}
				}
			}
			if m.screen != tt.screen || m.help != tt.help || quit != tt.quit {
				t.Errorf("screen %d help %v quit %v, want %d %v %v", m.screen, m.help, quit, tt.screen, tt.help, tt.quit)
			}
			if got := m.stacks.selected(&m.d); got != tt.selected {
				t.Errorf("selected %q, want %q", got, tt.selected)
			}
			if got := m.stacks.filter.Value(); got != tt.filter {
				t.Errorf("filter %q, want %q", got, tt.filter)
			}
		})
	}
}

func app(name, stack, sync, health string) kube.App {
	return kube.App{Name: name, Labels: map[string]string{gitops.StackLabel: stack}, Sync: sync, Health: health, Current: true}
}

var upCluster = cluster{known: true, up: true, nodes: 3, nodesReady: 3, hosts: []string{"grafana.localtest.me", "podinfo.localtest.me"},
	apps: []kube.App{
		app("cert-manager", "core", "Synced", "Healthy"),
		app("envoy-gateway", "core", "Synced", "Healthy"),
		app("lab-bootstrap", "gitops", "Synced", "Healthy"),
		app("loki", "obs", "OutOfSync", "Progressing"),
		app("podinfo-frontend", "sample-app", "Synced", "Degraded"),
	}}

func TestStackStatus(t *testing.T) {
	failed := labState(0)
	failed.Stacks["obs"] = state.StackState{Status: state.Failed, Error: "context canceled"}
	tests := []struct {
		name  string
		stack string
		st    state.State
		c     cluster
		want  status
		text  string
	}{
		{"not deployed", "policy", labState(0), upCluster, statusOff, "not deployed"},
		{"failed in state", "obs", failed, upCluster, statusFailed, "failed"},
		{"before the first report, trust state", "core", labState(0), cluster{}, statusHealthy, "applied"},
		{"cluster down is off, not an error", "core", labState(0), cluster{known: true, reason: "no cluster"}, statusOff, "cluster off"},
		{"registry has no apps", "registry", labState(0), upCluster, statusHealthy, "applied"},
		{"nodes all Ready", "cluster", labState(0), upCluster, statusHealthy, "3/3 nodes Ready"},
		{"a node not Ready", "cluster", labState(0), cluster{known: true, up: true, nodes: 3, nodesReady: 2}, statusProgressing, "2/3 nodes Ready"},
		{"all apps ready", "core", labState(0), upCluster, statusHealthy, "2/2 apps ready"},
		{"gitops is the root app", "gitops", labState(0), upCluster, statusHealthy, "1/1 apps ready"},
		{"an app syncing", "obs", labState(0), upCluster, statusProgressing, "0/1 apps ready"},
		{"a degraded app", "sample-app", labState(0), upCluster, statusFailed, "0/1 apps ready"},
		{"applied but no apps yet", "core", labState(0), cluster{known: true, up: true}, statusProgressing, "waiting for Argo CD apps"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, text := stackStatus(tt.stack, tt.st, tt.c)
			if got != tt.want || text != tt.text {
				t.Errorf("got %s %q, want %s %q", statusLooks[got].word, text, statusLooks[tt.want].word, tt.text)
			}
		})
	}
}

func TestHome(t *testing.T) {
	onMain := labState(7)
	onMain.Revision = "main"
	at := func(minutes int) string {
		return t0.Add(time.Duration(minutes) * time.Minute).Local().Format("Jan 02 15:04")
	}
	tests := []struct {
		name   string
		st     state.State
		c      cluster
		want   []string
		absent []string
	}{
		{"empty state", state.New(), cluster{known: true, reason: "no cluster"},
			[]string{"Nothing is deployed.", "press U to bring the lab up", "health: ○ off", "ops ▸ none yet"}, []string{"error"}},
		{"cluster down", labState(1), cluster{known: true, reason: "not reachable: connection refused"},
			[]string{"cluster    ○ off (not reachable: connection refused)", "stacks     ○ off — cluster not running",
				"press U to bring the lab up", "shown once the cluster is up"}, []string{"error"}},
		{"connecting", labState(1), cluster{}, []string{"health: ◐ connecting", "cluster    ◐ connecting"}, nil},
		{"branch revision", labState(1), upCluster, []string{"rev: feat/x", "revision   feat/x (not main)"}, nil},
		{"up on main", onMain, upCluster, []string{
			"rev: main", "health: ✕ 3/5 apps", "nodes      ● 3/3 Ready", "apps       ● 3/5 ready  ◐ 1 progressing  ✕ 1 failed",
			"https://grafana.localtest.me:8443", "https://podinfo.localtest.me:8443", "on the Mac: ssh -N fedora-lab",
			at(6) + "  lab up  core apply  41s", at(2)}, []string{"(not main)", at(1), emptyHint}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(tt.st)
			m.d.live = tt.c
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

func TestStacksDetail(t *testing.T) {
	m := newModel(labState(1))
	m.d.live = upCluster
	for _, k := range []string{"2", "/", "o", "b", "s", "enter"} {
		next, _ := m.Update(key(k))
		m = next.(Model)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	view := plain(next.(Model))
	for _, want := range []string{"obs  ◐ progressing", "depends on  gitops, core", "Argo CD apps", "◐ loki  OutOfSync  Progressing", "◐ obs"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
}

// A kubeconfig that appears or changes restarts the watcher; reports from
// the previous one are dropped.
func TestReconnect(t *testing.T) {
	dir := t.TempDir()
	kc := filepath.Join(dir, "kubeconfig")
	m := New(Options{Config: config.Default(), State: labState(0), StatePath: filepath.Join(dir, "state.json"), Kubeconfig: kc,
		Catalog: engine.Catalog(), Connect: func(string) (*kube.Clients, error) { return nil, fmt.Errorf("unused") }})

	next, _ := m.Update(m.connect(m.gen)())
	m = next.(Model)
	if m.d.live.reason != "no cluster" || m.d.live.up {
		t.Fatalf("missing kubeconfig: live %+v, want off with no cluster", m.d.live)
	}
	if err := os.WriteFile(kc, []byte("apiVersion: v1\nkind: Config\ncurrent-context: kind-lab\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	next, cmd := m.Update(m.checkFiles()())
	m = next.(Model)
	if m.gen != 2 || cmd == nil {
		t.Fatalf("gen %d, want 2 after the kubeconfig appeared", m.gen)
	}
	next, _ = m.Update(liveMsg{gen: 1, c: upCluster})
	m = next.(Model)
	if m.d.live.up {
		t.Error("a report from the old watcher was applied")
	}
	next, _ = m.Update(liveMsg{gen: 2, c: upCluster})
	if !next.(Model).d.live.up {
		t.Error("a report from the current watcher was dropped")
	}
}

// TestHeaderSaysHalfBuiltCloudLabIsBilled: a cloud lab with a failed stack
// says in the header that it is still billed; local failures do not.
func TestHeaderSaysHalfBuiltCloudLabIsBilled(t *testing.T) {
	for _, mode := range []string{"cloud", "local"} {
		st := labState(0)
		st.Mode = mode
		st.Stacks["cluster"] = state.StackState{Status: state.Failed, Error: "tofu apply: exit status 1"}
		m := newModel(st)
		got := strings.Contains(ansi.Strip(m.header()), "✕ "+state.BilledWarning)
		if got != (mode == "cloud") {
			t.Errorf("%s: header warns = %v: %q", mode, got, ansi.Strip(m.header()))
		}
	}
}
