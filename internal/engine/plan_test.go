package engine

import (
	"slices"
	"strings"
	"testing"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/state"
)

func catalogGraph(t *testing.T) *Graph {
	t.Helper()
	g, err := NewGraph(Catalog())
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func cfgFor(mode, profile string) config.Config {
	c := config.Default()
	c.Mode, c.Profile = mode, profile
	return c
}

// stateWith builds a state; values are stack statuses.
func stateWith(mode string, stacks map[string]string) state.State {
	s := state.New()
	s.Mode = mode
	for name, status := range stacks {
		s.Stacks[name] = state.StackState{Status: status, Error: map[bool]string{true: "timeout"}[status == state.Failed]}
	}
	return s
}

// render turns steps into "+core =gitops -ai" for compact comparisons.
func render(p Plan) string {
	sym := map[Action]string{Apply: "+", Destroy: "-", Keep: "="}
	var parts []string
	for _, s := range p.Steps {
		parts = append(parts, sym[s.Action]+s.Stack)
	}
	return strings.Join(parts, " ")
}

func TestPlanUp(t *testing.T) {
	applied := state.Applied
	minimalApplied := map[string]string{
		"cluster": applied, "gitops": applied, "core": applied, "registry": applied, "sample-app": applied,
	}
	// minimal plus stacks from a previous full run.
	withExtras := map[string]string{
		"cluster": applied, "gitops": applied, "core": applied, "registry": applied, "sample-app": applied,
		"obs": applied, "chaos": applied, "secrets": state.Failed,
	}
	const keepWarning = "3 deployed stacks are not in profile minimal, kept: obs, secrets, chaos. Run lab up --prune to remove."
	tests := []struct {
		name         string
		cfg          config.Config
		st           state.State
		prune        bool
		want         string
		wantChanges  int
		wantWarnings []string
		wantErr      string
	}{
		{
			name:        "fresh minimal",
			cfg:         cfgFor("local", "minimal"),
			st:          state.New(),
			want:        "+registry +cluster +gitops +core +sample-app",
			wantChanges: 5,
		},
		{
			name:        "fresh standard",
			cfg:         cfgFor("local", "standard"),
			st:          state.New(),
			want:        "+registry +cluster +gitops +core +secrets +obs +policy +scaling +sample-app",
			wantChanges: 9,
		},
		{
			name: "fully applied is all keep",
			cfg:  cfgFor("local", "minimal"),
			st:   stateWith("local", minimalApplied),
			want: "=registry =cluster =gitops =core =sample-app",
		},
		{
			name:        "partial apply resumes",
			cfg:         cfgFor("local", "minimal"),
			st:          stateWith("local", map[string]string{"registry": applied, "cluster": applied, "gitops": applied}),
			want:        "=registry =cluster =gitops +core +sample-app",
			wantChanges: 2,
		},
		{
			name:        "failed stack is retried",
			cfg:         cfgFor("local", "minimal"),
			st:          stateWith("local", map[string]string{"registry": applied, "cluster": applied, "gitops": state.Failed}),
			want:        "=registry =cluster +gitops +core +sample-app",
			wantChanges: 3,
		},
		{
			name:         "smaller profile keeps extras and warns",
			cfg:          cfgFor("local", "minimal"),
			st:           stateWith("local", withExtras),
			want:         "=registry =cluster =gitops =core =sample-app =obs =secrets =chaos",
			wantWarnings: []string{keepWarning},
		},
		{
			name:        "smaller profile with prune destroys extras, dependents first",
			cfg:         cfgFor("local", "minimal"),
			st:          stateWith("local", withExtras),
			prune:       true,
			want:        "-chaos -secrets -obs =registry =cluster =gitops =core =sample-app",
			wantChanges: 3,
		},
		{
			name: "one extra uses singular wording",
			cfg:  cfgFor("local", "minimal"),
			st: stateWith("local", map[string]string{
				"cluster": applied, "gitops": applied, "core": applied, "registry": applied, "sample-app": applied, "policy": applied,
			}),
			want:         "=registry =cluster =gitops =core =sample-app =policy",
			wantWarnings: []string{"1 deployed stack is not in profile minimal, kept: policy. Run lab up --prune to remove."},
		},
		{
			name:        "prune with nothing extra changes nothing",
			cfg:         cfgFor("local", "minimal"),
			st:          stateWith("local", minimalApplied),
			prune:       true,
			want:        "=registry =cluster =gitops =core =sample-app",
			wantChanges: 0,
		},
		{
			name:    "state from the other mode",
			cfg:     cfgFor("cloud", "minimal"),
			st:      stateWith("local", map[string]string{"cluster": applied}),
			wantErr: "run lab down in local mode first",
		},
		{
			name: "other mode with nothing deployed is fine",
			cfg:  cfgFor("cloud", "minimal"),
			st:   stateWith("local", nil),
			want: "+registry +cluster +gitops +core +sample-app", wantChanges: 5,
		},
		{
			name:    "unknown stack in state",
			cfg:     cfgFor("local", "minimal"),
			st:      stateWith("local", map[string]string{"harbor": applied}),
			wantErr: `stack "harbor", which is not in the catalog`,
		},
	}
	g := catalogGraph(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := PlanUp(g, tt.cfg, tt.st, PlanOptions{Prune: tt.prune})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := render(p); got != tt.want {
				t.Errorf("plan = %s\nwant   %s", got, tt.want)
			}
			if p.Changes() != tt.wantChanges {
				t.Errorf("changes = %d, want %d", p.Changes(), tt.wantChanges)
			}
			if !slices.Equal(p.Warnings, tt.wantWarnings) {
				t.Errorf("warnings = %q\nwant       %q", p.Warnings, tt.wantWarnings)
			}
			for _, s := range p.Steps {
				if s.Action == Keep && !slices.Contains(mustProfile(t, tt.cfg.Profile), s.Stack) && s.Reason != keepReason {
					t.Errorf("kept extra %s has reason %q, want %q", s.Stack, s.Reason, keepReason)
				}
			}
			if p.K8sVersion != config.K8sVersion || p.Mode != tt.cfg.Mode || p.Profile != tt.cfg.Profile {
				t.Errorf("plan header = %s/%s/%s", p.Mode, p.Profile, p.K8sVersion)
			}
		})
	}
}

func mustProfile(t *testing.T, profile string) []string {
	t.Helper()
	s, err := ProfileStacks(profile)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPlanUpFailedReasonCarriesError(t *testing.T) {
	p, err := PlanUp(catalogGraph(t), cfgFor("local", "minimal"), stateWith("local", map[string]string{"registry": state.Failed}), PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Steps[0].Reason != "retry: timeout" {
		t.Errorf("reason = %q", p.Steps[0].Reason)
	}
}

func TestPlanDown(t *testing.T) {
	applied := state.Applied
	tests := []struct {
		name    string
		st      state.State
		want    string
		wantErr string
	}{
		{"nothing deployed", state.New(), "", ""},
		{"reverse dependency order", stateWith("local", map[string]string{
			"cluster": applied, "gitops": applied, "core": applied, "registry": applied, "obs": applied, "sample-app": applied,
		}), "-sample-app -core -obs -gitops -cluster -registry", ""},
		{"failed stacks are destroyed too", stateWith("local", map[string]string{
			"cluster": applied, "gitops": state.Failed,
		}), "-gitops -cluster", ""},
		{"other mode refused", stateWith("cloud", map[string]string{"cluster": applied}), "", "cloud lab"},
	}
	g := catalogGraph(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := PlanDown(g, cfgFor("local", "standard"), tt.st)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := render(p); got != tt.want {
				t.Errorf("plan = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlanUpStacks(t *testing.T) {
	applied := state.Applied
	tests := []struct {
		name    string
		stacks  []string
		prune   bool
		st      state.State
		want    string
		wantErr string
	}{
		{name: "selection pulls in dependencies", stacks: []string{"cluster"}, st: state.New(),
			want: "+registry +cluster"},
		{name: "cluster and registry", stacks: []string{"cluster", "registry"}, st: state.New(),
			want: "+registry +cluster"},
		{name: "deployed stacks outside the selection are kept", stacks: []string{"registry"},
			st:   stateWith("local", map[string]string{"registry": applied, "cluster": applied, "gitops": applied}),
			want: "=registry =cluster =gitops"},
		{name: "selection outside the profile is allowed", stacks: []string{"chaos"}, st: state.New(),
			want: "+registry +cluster +gitops +obs +chaos"},
		{name: "unknown stack", stacks: []string{"harbor"}, st: state.New(), wantErr: `unknown stack "harbor"`},
		{name: "prune with a selection is refused", stacks: []string{"cluster"}, prune: true, st: state.New(),
			wantErr: "cannot be combined with a stack selection"},
	}
	g := catalogGraph(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := PlanUp(g, cfgFor("local", "minimal"), tt.st, PlanOptions{Stacks: tt.stacks, Prune: tt.prune})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := render(p); got != tt.want {
				t.Errorf("plan = %s, want %s", got, tt.want)
			}
			if len(p.Warnings) > 0 {
				t.Errorf("unexpected warnings: %q", p.Warnings)
			}
			for _, s := range p.Steps {
				if s.Action == Keep && !slices.Contains(tt.stacks, s.Stack) && s.Reason != "not selected" && s.Reason != "applied" {
					t.Errorf("%s reason = %q", s.Stack, s.Reason)
				}
			}
		})
	}
}
