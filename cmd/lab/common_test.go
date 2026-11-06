package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/state"
)

// The active stacks: profile, extraStacks, asked for and deployed, with
// their dependencies.
func TestActiveStacks(t *testing.T) {
	home := t.TempDir()
	if err := state.NewStore(filepath.Join(home, "state.json")).Update(func(s *state.State) error {
		s.Stacks["policy"] = state.StackState{Status: state.Applied}
		s.Stacks["obs"] = state.StackState{Status: state.Failed}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	minimal := config.Default()
	minimal.ExtraStacks = nil
	tests := []struct {
		name  string
		cfg   config.Config
		home  string
		asked []string
		want  []string
		not   []string
	}{
		{"minimal profile", minimal, t.TempDir(), nil, []string{"registry", "cluster", "gitops", "core", "sample-app"}, []string{"policy", "obs"}},
		{"extraStacks", config.Default(), t.TempDir(), nil, []string{"obs", "core"}, []string{"policy"}},
		{"asked for, with its dependencies", minimal, t.TempDir(), []string{"policy"}, []string{"policy", "gitops"}, []string{"obs"}},
		{"deployed (failed ones do not count)", minimal, home, nil, []string{"policy"}, []string{"obs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := activeStacks(tt.cfg, tt.home, tt.asked)
			for _, w := range tt.want {
				if !slices.Contains(got, w) {
					t.Errorf("active %v lacks %s", got, w)
				}
			}
			for _, n := range tt.not {
				if slices.Contains(got, n) {
					t.Errorf("active %v has %s", got, n)
				}
			}
		})
	}
}

// The ai stack runs in local mode only: a cloud plan skips it (no GPU node
// yet), so it is never "to apply" and then refused.
func TestStackSupportAI(t *testing.T) {
	tests := []struct {
		mode string
		want map[string]bool
	}{
		{"local", map[string]bool{"ai": true, "obs": true, "secrets": true, "cluster": true}},
		{"cloud", map[string]bool{"ai": false, "obs": true, "secrets": true, "cluster": true}},
	}
	for _, tt := range tests {
		supports := stackSupport(config.Config{Mode: tt.mode, GitOps: "argocd"})
		for stack, want := range tt.want {
			if got := supports(stack); got != want {
				t.Errorf("%s: supports(%s) = %v, want %v", tt.mode, stack, got, want)
			}
		}
	}
}
