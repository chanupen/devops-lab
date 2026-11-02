package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	out, err := runLab(append([]string{"labs", "list"}, flags...)...)
	if err != nil || !strings.Contains(out, "○  01-argocd-bootstrap  Bootstrap Argo CD from scratch  45m   beginner  scratch  not started") {
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
