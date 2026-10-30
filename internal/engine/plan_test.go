package engine

import (
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
	tests := []struct {
		name        string
		cfg         config.Config
		st          state.State
		want        string
		wantChanges int
		wantErr     string
	}{
		{
			name:        "fresh minimal",
			cfg:         cfgFor("local", "minimal"),
			st:          state.New(),
			want:        "+core +gitops +registry +sample-app",
			wantChanges: 4,
		},
		{
			name:        "fresh standard",
			cfg:         cfgFor("local", "standard"),
			st:          state.New(),
			want:        "+core +gitops +registry +secrets +obs +policy +scaling +sample-app",
			wantChanges: 8,
		},
		{
			name: "fully applied is all keep",
			cfg:  cfgFor("local", "minimal"),
			st: stateWith("local", map[string]string{
				"core": applied, "gitops": applied, "registry": applied, "sample-app": applied,
			}),
			want: "=core =gitops =registry =sample-app",
		},
		{
			name:        "partial apply resumes",
			cfg:         cfgFor("local", "minimal"),
			st:          stateWith("local", map[string]string{"core": applied, "gitops": applied}),
			want:        "=core =gitops +registry +sample-app",
			wantChanges: 2,
		},
		{
			name:        "failed stack is retried",
			cfg:         cfgFor("local", "minimal"),
			st:          stateWith("local", map[string]string{"core": applied, "gitops": state.Failed}),
			want:        "=core +gitops +registry +sample-app",
			wantChanges: 3,
		},
		{
			name: "smaller profile destroys extras, dependents first",
			cfg:  cfgFor("local", "minimal"),
			st: stateWith("local", map[string]string{
				"core": applied, "gitops": applied, "registry": applied, "sample-app": applied,
				"obs": applied, "chaos": applied, "secrets": state.Failed,
			}),
			want:        "-chaos -secrets -obs =core =gitops =registry =sample-app",
			wantChanges: 3,
		},
		{
			name:    "state from the other mode",
			cfg:     cfgFor("cloud", "minimal"),
			st:      stateWith("local", map[string]string{"core": applied}),
			wantErr: "run lab down in local mode first",
		},
		{
			name: "other mode with nothing deployed is fine",
			cfg:  cfgFor("cloud", "minimal"),
			st:   stateWith("local", nil),
			want: "+core +gitops +registry +sample-app", wantChanges: 4,
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
			p, err := PlanUp(g, tt.cfg, tt.st)
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
			if p.K8sVersion != config.K8sVersion || p.Mode != tt.cfg.Mode || p.Profile != tt.cfg.Profile {
				t.Errorf("plan header = %s/%s/%s", p.Mode, p.Profile, p.K8sVersion)
			}
		})
	}
}

func TestPlanUpFailedReasonCarriesError(t *testing.T) {
	p, err := PlanUp(catalogGraph(t), cfgFor("local", "minimal"), stateWith("local", map[string]string{"core": state.Failed}))
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
			"core": applied, "gitops": applied, "registry": applied, "obs": applied, "sample-app": applied,
		}), "-sample-app -registry -obs -gitops -core", ""},
		{"failed stacks are destroyed too", stateWith("local", map[string]string{
			"core": applied, "gitops": state.Failed,
		}), "-gitops -core", ""},
		{"other mode refused", stateWith("cloud", map[string]string{"core": applied}), "", "cloud lab"},
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
