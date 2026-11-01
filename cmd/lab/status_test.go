package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"slices"

	"github.com/chanupen/devops-lab/internal/config"
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
			[]string{"Apps:"}},
		{"on main with core lists apps",
			`{"version":1,"mode":"local","revision":"main","stacks":{"registry":{"status":"applied","updatedAt":"2026-10-31T00:30:00Z"},
			"cluster":{"status":"applied"},"gitops":{"status":"applied"},"core":{"status":"applied"}},"ops":[]}`,
			[]string{"gitops revision: main\n", "● registry", "● core", "○ obs", "Apps:\n  https://argocd.localtest.me:8443", "ssh -N fedora-lab"},
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
