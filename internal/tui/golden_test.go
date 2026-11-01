package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chanupen/devops-lab/internal/kube"
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

	tests := []struct {
		name  string
		st    state.State
		c     cluster
		keys  []string
		setup func(Model) Model
	}{
		{"home_empty", state.New(), cluster{known: true, reason: "no cluster"}, nil, nil},
		{"home_down", labState(2), cluster{known: true, reason: "not reachable: connection refused"}, nil, nil},
		{"home_up", onMain, upCluster, nil, nil},
		{"stacks_up", onMain, upCluster, []string{"2", "j", "j", "j"}, nil},
		{"stacks_failed", failed, degraded, []string{"2", "/", "o", "b", "s", "enter"}, nil},
		{"stacks_degraded_app", onMain, degraded, []string{"2", "G", "k"}, nil},
		{"home_deleted_branch", gone, upCluster, nil, func(m Model) Model { m.d.branchGone["feat/phase1-obs"] = true; return m }},
		{"ops_log", failed, upCluster, []string{"5"}, nil},
		{"ops_running", failed, upCluster, []string{"5"}, running},
		{"palette", onMain, upCluster, []string{":", "d", "o", "w"}, nil},
		{"help_stacks", onMain, upCluster, []string{"2", "?"}, nil},
		{"help_ops", onMain, upCluster, []string{"5", "?"}, nil},
		{"confirm_down", onMain, upCluster, []string{"2", "G", "k"}, func(m Model) Model {
			m.confirm = &confirmState{req: Request{Down: true, Stacks: []string{"sample-app"}}, question: "Destroy sample-app?"}
			return m
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(tt.st)
			m.d.live, m.d.ctx = tt.c, "kind-lab"
			m.d.cfgPath = "/home/user/devops-lab/lab.yaml"
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
