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
	degraded.apps[4].Problems = []string{"ComparisonError: revision feat/old not found"}

	tests := []struct {
		name string
		st   state.State
		c    cluster
		keys []string
	}{
		{"home_empty", state.New(), cluster{known: true, reason: "no cluster"}, nil},
		{"home_down", labState(2), cluster{known: true, reason: "not reachable: connection refused"}, nil},
		{"home_up", onMain, upCluster, nil},
		{"stacks_up", onMain, upCluster, []string{"2", "j", "j", "j"}},
		{"stacks_failed", failed, degraded, []string{"2", "/", "o", "b", "s", "enter"}},
		{"stacks_degraded_app", onMain, degraded, []string{"2", "G", "k"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(tt.st)
			m.d.live, m.d.ctx = tt.c, "kind-lab"
			var next tea.Model = m
			next, _ = next.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			for _, k := range tt.keys {
				next, _ = next.Update(key(k))
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
