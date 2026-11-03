package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"slices"

	"github.com/charmbracelet/lipgloss"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/kube"
	"github.com/chanupen/devops-lab/internal/state"
)

func TestStatusCmd(t *testing.T) {
	tests := []struct {
		name    string
		state   string // empty: no state file
		want    []string
		wantNot []string
	}{
		{"nothing deployed", "",
			[]string{"gitops revision: - (gitops not deployed)", "○ registry", "not deployed", "○ sample-app"},
			[]string{"Apps:", "live:"}},
		{"on main with core lists apps",
			`{"version":1,"mode":"local","revision":"main","stacks":{"registry":{"status":"applied","updatedAt":"2026-10-31T00:30:00Z"},
			"cluster":{"status":"applied"},"gitops":{"status":"applied"},"core":{"status":"applied"}},"ops":[]}`,
			[]string{"gitops revision: main\n", "● registry", "● core", "○ obs", "Apps:\n  https://argocd.localtest.me:8443", "ssh -N fedora-lab",
				"live:\n  cluster  ○ off (no kubeconfig at "},
			[]string{"not main"}},
		{"on a branch is highlighted",
			`{"version":1,"mode":"local","revision":"feat/obs","stacks":{"registry":{"status":"applied"},"cluster":{"status":"applied"},
			"gitops":{"status":"applied"}},"ops":[]}`,
			[]string{"gitops revision: feat/obs (not main; a plain lab up moves it back)"},
			[]string{"Apps:"}},
		{"unrecorded revision",
			`{"version":1,"mode":"local","stacks":{"gitops":{"status":"applied"}},"ops":[]}`,
			[]string{"gitops revision: unrecorded (lab up records it and moves gitops to main)"}, nil},
		{"failed stack shows its error",
			`{"version":1,"mode":"local","stacks":{"registry":{"status":"failed","error":"port 5000 already in use"}},"ops":[]}`,
			[]string{"✕ registry", "failed: port 5000 already in use"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath, statePath := filepath.Join(dir, "lab.yaml"), filepath.Join(dir, "state.json")
			if err := os.WriteFile(cfgPath, []byte("profile: minimal\nextraStacks: []\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.state != "" {
				if err := os.WriteFile(statePath, []byte(tt.state), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fp := &fakeProvider{urls: []string{"https://argocd.localtest.me:8443"}}
			cmd := newRootCmdWith(func(config.Config, string, runOptions) (provider, error) { return fp, nil })
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"status", "--config", cfgPath, "--state", statePath})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}
			for _, w := range tt.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, out.String())
				}
			}
			for _, w := range tt.wantNot {
				if strings.Contains(out.String(), w) {
					t.Errorf("output should not contain %q:\n%s", w, out.String())
				}
			}
			if _, err := os.Stat(statePath); tt.state == "" && err == nil {
				t.Error("status wrote a state file")
			}
		})
	}
}

func TestUpRevisionFlag(t *testing.T) {
	dir := t.TempDir()
	cfgPath, statePath := filepath.Join(dir, "lab.yaml"), filepath.Join(dir, "state.json")
	if err := os.WriteFile(cfgPath, []byte("profile: minimal\nextraStacks: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotRev string
	fp := &fakeProvider{}
	cmd := newRootCmdWith(func(_ config.Config, _ string, o runOptions) (provider, error) { gotRev = o.Revision; return fp, nil })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"up", "--stacks", "cluster", "--revision", "feat/obs", "--config", cfgPath, "--state", statePath})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--revision applies to the gitops stack") {
		t.Fatalf("err = %v; --revision without gitops in the selection should fail", err)
	}
	if gotRev != "" {
		t.Errorf("provider was built (revision %q) before the plan was rejected", gotRev)
	}
}

func TestRevisionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath, statePath := filepath.Join(dir, "lab.yaml"), filepath.Join(dir, "state.json")
	if err := os.WriteFile(cfgPath, []byte("profile: minimal\nextraStacks: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fp := &fakeProvider{also: []string{"gitops"}}
	steps := []struct {
		args       []string
		wantRev    string // passed to the provider
		wantState  string // recorded afterwards
		wantGitOps bool   // gitops applied in this run
		wantOut    string
	}{
		{[]string{"--revision", "feat/obs"}, "feat/obs", "feat/obs", true, "gitops will track feat/obs, not main"},
		{[]string{"--revision", "feat/obs"}, "feat/obs", "feat/obs", false, "nothing to do"},
		{nil, "", "main", true, "revision feat/obs -> main"},
		{nil, "", "main", false, "nothing to do"},
	}
	for i, s := range steps {
		var gotRev string
		before := len(fp.calls)
		cmd := newRootCmdWith(func(_ config.Config, _ string, o runOptions) (provider, error) { gotRev = o.Revision; return fp, nil })
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(append([]string{"up", "--stacks", "gitops", "--config", cfgPath, "--state", statePath}, s.args...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("step %d: %v\n%s", i, err, out.String())
		}
		if gotRev != s.wantRev {
			t.Errorf("step %d: provider revision = %q, want %q", i, gotRev, s.wantRev)
		}
		applied := slices.Contains(fp.calls[before:], "apply gitops")
		if applied != s.wantGitOps {
			t.Errorf("step %d: gitops applied = %v, want %v (calls %v)", i, applied, s.wantGitOps, fp.calls[before:])
		}
		st, err := state.NewStore(statePath).Load()
		if err != nil {
			t.Fatal(err)
		}
		if st.Revision != s.wantState {
			t.Errorf("step %d: state revision = %q, want %q", i, st.Revision, s.wantState)
		}
		if !strings.Contains(out.String(), s.wantOut) {
			t.Errorf("step %d: output lacks %q:\n%s", i, s.wantOut, out.String())
		}
	}
}

func TestWriteLive(t *testing.T) {
	healthy := kube.App{Name: "podinfo", Sync: "Synced", Health: "Healthy", Current: true}
	tests := []struct {
		name    string
		h       kube.Health
		err     error
		stopped string
		want    []string
		wantNot []string
	}{
		{"all healthy", kube.Health{Nodes: 3, Ready: 3, Apps: []kube.App{healthy, {Name: "grafana", Sync: "Synced", Health: "Healthy", Current: true}}}, nil, "",
			[]string{"live:\n", "  nodes    ● 3/3 Ready\n", "  apps     ● 2/2 Synced and Healthy\n"}, []string{"◐", "✕"}},
		{"a node not ready and two apps not", kube.Health{Nodes: 2, Ready: 1, Apps: []kube.App{healthy,
			{Name: "loki", Sync: "OutOfSync", Health: "Progressing"},
			{Name: "tempo", Sync: "Synced", Health: "Degraded", Problems: []string{"SyncError: one or more objects failed"}}}}, nil, "",
			[]string{"  nodes    ◐ 1/2 Ready", "  apps     ◐ 1/3 Synced and Healthy", "◐ loki  OutOfSync  Progressing",
				"✕ tempo  Synced  Degraded  (SyncError: one or more objects failed)"}, []string{"podinfo"}},
		{"before Argo CD", kube.Health{Nodes: 1, Ready: 1}, nil, "", []string{"● 1/1 Ready", "apps     ○ Argo CD is not installed"}, nil},
		{"cluster down", kube.Health{}, errors.New("list nodes: dial tcp 127.0.0.1:6443: connect: connection refused\nmore"), "",
			[]string{"  cluster  ○ off (list nodes: dial tcp 127.0.0.1:6443: connect: connection refused)\n"}, []string{"more", "Ready"}},
		{"cluster stopped by a reboot", kube.Health{}, errors.New("refused"), "podman start lab-control-plane lab-worker",
			[]string{"  cluster  ○ stopped; lab up starts it (or podman start lab-control-plane lab-worker)"}, []string{"off"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := writeLive(&out, lipgloss.NewRenderer(&out), tt.h, tt.err, tt.stopped); err != nil {
				t.Fatal(err)
			}
			for _, w := range tt.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("lacks %q:\n%s", w, out.String())
				}
			}
			for _, w := range tt.wantNot {
				if strings.Contains(out.String(), w) {
					t.Errorf("has %q:\n%s", w, out.String())
				}
			}
		})
	}
}

// stoppedFix finds the main cluster's stopped nodes through Podman.
func TestStoppedFix(t *testing.T) {
	tests := []struct {
		name       string
		containers map[string]fakeContainer
		want       string
	}{
		{"stopped by a reboot", map[string]fakeContainer{"lab-control-plane": {}, "lab-worker": {}, "lab-zot": {}},
			"podman start lab-control-plane lab-worker"},
		{"running", map[string]fakeContainer{"lab-control-plane": {Running: true}}, ""},
		{"no cluster", map[string]fakeContainer{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakePodmanSocket(t, tt.containers)
			if got := stoppedFix(context.Background()); got != tt.want {
				t.Errorf("stoppedFix = %q, want %q", got, tt.want)
			}
		})
	}
	t.Run("no Podman", func(t *testing.T) {
		t.Setenv("CONTAINER_HOST", "unix:///nonexistent/podman.sock")
		if got := stoppedFix(context.Background()); got != "" {
			t.Errorf("stoppedFix = %q", got)
		}
	})
}
