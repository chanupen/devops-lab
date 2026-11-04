package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chanupen/devops-lab/internal/drills"
	"github.com/chanupen/devops-lab/internal/kube"
	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/state"
)

// update rewrites testdata/*.golden: go test ./internal/tui -run Golden -update
var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// The golden files hold each screen as plain text at 100×30. Colours and
// reverse video are stripped so diffs read well; the symbols carry the state.
func TestGolden(t *testing.T) {
	// Times render in local time; pin it so the files match on any host.
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })

	onMain := labState(7)
	onMain.Revision = "main"
	failed := labState(3)
	failed.Stacks["obs"] = state.StackState{Status: state.Failed, UpdatedAt: t0.Add(5 * time.Minute),
		Error: "helm install loki: context deadline exceeded\nmore detail"}
	failed.AppendOp(state.Op{Command: "lab up", Stack: "obs", Action: "apply", Status: "failed",
		Started: t0.Add(4 * time.Minute), Finished: t0.Add(9 * time.Minute), Error: "context deadline exceeded"})
	degraded := upCluster
	degraded.apps = append([]kube.App(nil), upCluster.apps...)
	degraded.apps[4].Problems = []string{"ComparisonError: unable to resolve 'feat/phase1-obs' to a commit SHA",
		"SyncError: one or more objects failed to apply"}
	gone := labState(2)
	gone.Revision = "feat/phase1-obs"
	running := func(m Model) Model {
		m.op = &opState{req: Request{Stacks: []string{"sample-app"}}, started: t0, step: "sample-app: applying",
			last: "02:25:41.200 waiting for podinfo-frontend: OutOfSync Progressing", lines: newRing(logLines), cancel: func() {}}
		m.opts.Now = func() time.Time { return t0.Add(42 * time.Second) }
		return m
	}

	// The Labs screen renders the repo's lab 1, started 20 minutes ago and
	// on step 3, with a failed verify of that step on record.
	lab1, err := labs.LoadAll(filepath.Join("..", "..", "labs"))
	if err != nil {
		t.Fatal(err)
	}
	onLab := labState(2)
	onLab.ActiveLab = "01-argocd-bootstrap"
	onLab.Labs = map[string]state.LabProgress{"01-argocd-bootstrap": {Step: 3, Attempts: map[int]int{1: 2, 2: 1, 3: 1}, Hints: map[int]int{3: 1},
		Started: t0.Add(-20 * time.Minute), Session: t0.Add(-12 * time.Minute), Spent: 8 * time.Minute}}
	failedVerify := func(m Model) Model {
		m.labs.results["01-argocd-bootstrap/3"] = labs.Result{Step: 3, Reason: "podinfo is OutOfSync, not Synced: argocd app sync podinfo"}
		return m
	}

	// The Drills screen renders the repo's drills: one running 4m12s with
	// its alert fired and an ack, one random, and the list with history.
	allDrills, err := drills.LoadAll(filepath.Join("..", "..", "drills"))
	if err != nil {
		t.Fatal(err)
	}
	drilling := labState(7)
	drilling.Stacks["obs"] = state.StackState{Status: state.Applied}
	drilling.ActiveDrill = &state.DrillRun{Run: "r1", Drill: "oom", Phase: state.DrillRunning, Started: t0.Add(-4*time.Minute - 12*time.Second),
		Deadline: t0.Add(26*time.Minute - 12*time.Second), Alert: "KubeContainerOOMKilled", AlertFired: t0.Add(-2 * time.Minute),
		Acked: t0.Add(-time.Minute), Hints: 1}
	randomDrill := labState(7)
	randomDrill.ActiveDrill = &state.DrillRun{Run: "r2", Drill: "dns", Random: true, Phase: state.DrillRunning, Started: t0.Add(-time.Minute),
		Deadline: t0.Add(29 * time.Minute), Alert: "CoreDNSDown"}
	drillHistory := labState(7)
	drillHistory.Stacks["obs"] = state.StackState{Status: state.Applied}
	drillHistory.Drills = []state.DrillRun{{Run: "r0", Drill: "bad-deploy", Started: t0.Add(-time.Hour), Acked: t0.Add(-58 * time.Minute),
		Recovered: t0.Add(-55 * time.Minute), Outcome: state.DrillRecovered}}

	tests := []struct {
		name  string
		st    state.State
		c     cluster
		keys  []string
		setup func(Model) Model
	}{
		// Cases named *labs*: lab 1 is loaded and the clock pinned before the keys.
		{"labs_not_started", labState(2), upCluster, []string{"3"}, nil},
		{"labs_started", onLab, upCluster, []string{"3"}, nil},
		{"labs_step", onLab, upCluster, []string{"3", "enter"}, failedVerify},
		{"labs_next_step", onLab, upCluster, []string{"3", "enter", "n"}, nil},
		{"help_labs", onLab, upCluster, []string{"3", "?"}, nil},
		// Cases named *drills*: the repo's drills are loaded and the clock pinned.
		{"drills_list", drillHistory, upCluster, []string{"4"}, nil},
		{"drills_running", drilling, upCluster, []string{"4"}, nil},
		{"drills_random", randomDrill, upCluster, []string{"4"}, nil},
		{"help_drills", drilling, upCluster, []string{"4", "?"}, nil},
		{"home_empty", state.New(), cluster{known: true, reason: "no cluster"}, nil, nil},
		{"home_down", labState(2), cluster{known: true, reason: "not reachable: connection refused"}, nil, nil},
		{"home_up", onMain, upCluster, nil, nil},
		{"stacks_up", onMain, upCluster, []string{"2", "j", "j", "j"}, nil},
		{"stacks_failed", failed, degraded, []string{"2", "/", "o", "b", "s", "enter"}, nil},
		{"stacks_degraded_app", onMain, degraded, []string{"2", "G", "k", "k"}, nil},
		{"home_deleted_branch", gone, upCluster, nil, func(m Model) Model { m.d.branchGone["feat/phase1-obs"] = true; return m }},
		{"ops_log", failed, upCluster, []string{"5"}, nil},
		{"ops_running", failed, upCluster, []string{"5"}, running},
		{"palette", onMain, upCluster, []string{":", "d", "o", "w"}, nil},
		{"help_stacks", onMain, upCluster, []string{"2", "?"}, nil},
		{"help_ops", onMain, upCluster, []string{"5", "?"}, nil},
		{"home_read_only", onMain, upCluster, nil, func(m Model) Model {
			m.d.readOnly = "lab up --stacks obs (pid 42 on /dev/pts/3, since 17:20)"
			return m
		}},
		{"confirm_down", onMain, upCluster, []string{"2", "G", "k", "k"}, func(m Model) Model {
			m.confirm = &confirmState{req: Request{Down: true, Stacks: []string{"sample-app"}}, question: "Destroy sample-app?"}
			return m
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(tt.st)
			m.d.live, m.d.ctx = tt.c, "kind-lab"
			m.d.cfgPath = "/home/user/devops-lab/lab.yaml"
			if strings.Contains(tt.name, "labs") {
				m.d.labs = lab1
				m.opts.Now = func() time.Time { return t0 }
			}
			if strings.Contains(tt.name, "drills") {
				m.d.drills = allDrills
				m.opts.Now = func() time.Time { return t0 }
			}
			var next tea.Model = m
			next, _ = next.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			for _, k := range tt.keys {
				next, _ = next.Update(key(k))
			}
			if tt.setup != nil { // after the keys, so a prompt is not answered by them
				next = tt.setup(next.(Model))
			}
			got := plain(next.(Model)) + "\n"
			path := filepath.Join("testdata", tt.name+".golden")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got != string(want) {
				t.Errorf("%s differs (run with -update if the change is intended)\n--- got\n%s\n--- want\n%s",
					path, got, want)
			}
			if lines := strings.Count(got, "\n"); lines != 30 {
				t.Errorf("%d lines, want 30", lines)
			}
		})
	}
}
