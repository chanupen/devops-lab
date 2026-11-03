package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/state"
)

// lab labs list, status, hint and solution read the repo's labs/ next to
// lab.yaml and keep progress in the state file; none needs a cluster.
func TestLabsCommands(t *testing.T) {
	cfg, err := filepath.Abs(filepath.Join("..", "..", "lab.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "state.json")
	flags := []string{"--config", cfg, "--state", statePath}
	// The scratch node has run for an hour; the session began 15 minutes ago.
	fakePodmanSocket(t, map[string]fakeContainer{"lab-scratch-01-argocd-bootstrap-control-plane": {
		Running: true, StartedAt: time.Now().Add(-time.Hour)}})

	out, err := runLab(append([]string{"labs", "list"}, flags...)...)
	// Column widths follow the longest title, so compare the line's fields.
	if err != nil || !hasFields(out, "○ 01-argocd-bootstrap Bootstrap Argo CD from scratch 45m beginner scratch not started") {
		t.Errorf("list: %v\n%s", err, out)
	}
	if out, err := runLab(append([]string{"labs", "status"}, flags...)...); err != nil || !strings.Contains(out, "no lab is started") {
		t.Errorf("status with no lab: %v\n%s", err, out)
	}
	if _, err := runLab(append([]string{"labs", "verify"}, flags...)...); err == nil || !strings.Contains(err.Error(), "no lab is started") {
		t.Errorf("verify with no lab: %v", err)
	}

	// As if lab labs start 1 had run 15 minutes ago and step 1 passed.
	if err := state.NewStore(statePath).Update(func(s *state.State) error {
		s.ActiveLab = "01-argocd-bootstrap"
		s.Labs = map[string]state.LabProgress{"01-argocd-bootstrap": {Step: 2, Attempts: map[int]int{1: 1},
			Started: time.Now().Add(-15 * time.Minute), Session: time.Now().Add(-15 * time.Minute)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out, err = runLab(append([]string{"labs", "list"}, flags...)...)
	if err != nil || !strings.Contains(out, "◐  01-argocd-bootstrap") || !strings.Contains(out, "step 2/6, 15m, started") {
		t.Errorf("list while started: %v\n%s", err, out)
	}
	out, err = runLab(append([]string{"labs", "status"}, flags...)...)
	for _, want := range []string{"lab 01-argocd-bootstrap: Bootstrap Argo CD from scratch", "cluster: lab-scratch-01-argocd-bootstrap",
		"export KUBECONFIG=" + filepath.Join(filepath.Dir(statePath), "labs", "01-argocd-bootstrap", "kubeconfig"),
		"time spent: 15m of about 45m", "● 1  Install Argo CD with Helm  (1 attempt)", "◐ 2  Log in with the argocd CLI",
		"○ 6  Manage apps with an app of apps", "Step 2: Log in with the argocd CLI"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	if err != nil {
		t.Errorf("status: %v", err)
	}
	out, err = runLab(append([]string{"labs", "hint"}, flags...)...)
	if err != nil || !strings.HasPrefix(out, "Step 2. Hint 1 of 3: In a second terminal:") {
		t.Errorf("hint: %v\n%s", err, out)
	}
	// The solution asks first; without a terminal it needs --yes.
	if _, err := runLab(append([]string{"labs", "solution", "3"}, flags...)...); err == nil || !strings.Contains(err.Error(), "pass --yes") {
		t.Errorf("solution without --yes: %v", err)
	}
	out, err = runLab(append([]string{"labs", "solution", "3", "--yes"}, flags...)...)
	if err != nil || !strings.Contains(out, "kubectl apply -f 03-podinfo-app.yaml") || !strings.Contains(out, "--- solution/03-podinfo-app.yaml ---") {
		t.Errorf("solution: %v\n%s", err, out)
	}
	st, _ := state.NewStore(statePath).Load()
	if p := st.Labs["01-argocd-bootstrap"]; p.Hints[2] != 1 || len(p.Solutions) != 1 || p.Solutions[0] != 3 {
		t.Errorf("progress: %+v", p)
	}
}

// A failed lab labs start is in the op log with its output, like lab up.
func TestLabsStartIsAnOp(t *testing.T) {
	cfg, err := filepath.Abs(filepath.Join("..", "..", "lab.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("PATH", t.TempDir()) // no tools: start fails before any cluster
	_, err = runLab("labs", "start", "1", "--config", cfg, "--state", statePath)
	if err == nil || !strings.Contains(err.Error(), "on PATH") {
		t.Fatalf("start without tools: %v", err)
	}
	st, _ := state.NewStore(statePath).Load()
	if len(st.Ops) != 1 {
		t.Fatalf("ops = %+v", st.Ops)
	}
	op := st.Ops[0]
	if op.Command != "lab labs start 01" || op.Stack != "01-argocd-bootstrap" || op.Action != "labs start" || op.Status != "failed" || op.Log == "" {
		t.Errorf("op = %+v", op)
	}
}

func TestProgressText(t *testing.T) {
	l := labs.Lab{Steps: make([]labs.Step, 6)}
	tests := []struct {
		name   string
		p      state.LabProgress
		active bool
		spent  time.Duration
		want   string
	}{
		{"never started", state.LabProgress{}, false, 0, "not started"},
		{"just started: the current step, not 0/6", state.LabProgress{Step: 1}, true, 0, "step 1/6, 0m, started"},
		{"stopped at step 3", state.LabProgress{Step: 3}, false, 0, "step 3/6, 0m"},
		{"on the last step", state.LabProgress{Step: 6}, true, 95 * time.Minute, "step 6/6, 1h35m, started"},
		{"done, still started", state.LabProgress{Step: 7}, true, 42 * time.Minute, "done, 6/6 steps, 42m, started"},
		{"done and stopped", state.LabProgress{Step: 7}, false, 0, "done, 6/6 steps, 0m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := progressText(l, tt.p, tt.active, tt.spent); got != tt.want {
				t.Errorf("progressText = %q, want %q", got, tt.want)
			}
		})
	}
}

// lab labs start keeps kind's output in the op log; -v streams it too.
func TestOpLog(t *testing.T) {
	tests := []struct {
		verbose        bool
		terminalDetail bool
	}{{false, false}, {true, true}}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("verbose=%v", tt.verbose), func(t *testing.T) {
			var terminal, file strings.Builder
			w := newOpLog(&terminal, &file, tt.verbose)
			_, _ = io.WriteString(w, "creating kind cluster\n")
			_, _ = io.WriteString(w.Detail(), "kind: [v1] kubeadm config\n")
			if file.String() != "creating kind cluster\nkind: [v1] kubeadm config\n" {
				t.Errorf("op log = %q", file.String())
			}
			if got := strings.Contains(terminal.String(), "kubeadm"); got != tt.terminalDetail || !strings.Contains(terminal.String(), "creating kind cluster") {
				t.Errorf("terminal = %q", terminal.String())
			}
		})
	}
}

func TestStopMessage(t *testing.T) {
	scratch := labs.Lab{ID: "01-argocd-bootstrap", Needs: labs.NeedsScratch, Steps: make([]labs.Step, 6)}
	onMain := labs.Lab{ID: "02-promote", Needs: labs.NeedsLab, Steps: make([]labs.Step, 6)}
	tests := []struct {
		name string
		l    labs.Lab
		p    state.LabProgress
		want string
	}{
		{"scratch lab at step 4", scratch, state.LabProgress{Step: 4, Spent: 32 * time.Minute},
			"lab 01-argocd-bootstrap stopped at step 4/6 after 32m; its cluster is deleted, so lab labs start 01 begins at step 1"},
		{"scratch lab done", scratch, state.LabProgress{Step: 7, Spent: 41 * time.Minute},
			"lab 01-argocd-bootstrap stopped with all 6 steps done after 41m; its cluster is deleted, so lab labs start 01 begins at step 1"},
		{"main-cluster lab keeps its step", onMain, state.LabProgress{Step: 3, Spent: 5 * time.Minute},
			"lab 02-promote stopped at step 3/6 after 5m; lab labs start 02 picks up at step 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stopMessage(tt.l, tt.p); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

// hasFields reports whether a line of out has exactly want's fields.
func hasFields(out, want string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.Join(strings.Fields(line), " ") == want {
			return true
		}
	}
	return false
}
