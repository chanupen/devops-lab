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
	c.Mode, c.Profile, c.ExtraStacks = mode, profile, nil
	return c
}

// stateWith builds a state; values are stack statuses.
func stateWith(mode string, stacks map[string]string) state.State {
	s := state.New()
	s.Mode = mode
	for name, status := range stacks {
		s.Stacks[name] = state.StackState{Status: status, Error: map[bool]string{true: "timeout"}[status == state.Failed]}
	}
	if stacks[RevisionStack] == state.Applied {
		s.Revision = DefaultRevision // what a gitops apply records
	}
	return s
}

// render turns steps into "+core =gitops -ai" for compact comparisons.
func render(p Plan) string {
	sym := map[Action]string{Apply: "+", Destroy: "-", Keep: "=", Skip: "~"}
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
			want:        "+registry +cluster +gitops +core +secrets +obs +policy +scaling +sample-app +sample-db",
			wantChanges: 10,
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
			if p.K8sVersion != tt.cfg.ClusterK8sVersion() || p.Mode != tt.cfg.Mode || p.Profile != tt.cfg.Profile {
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
		}), "-sample-app -obs -core -gitops -cluster -registry", ""},
		{"failed stacks are destroyed too", stateWith("local", map[string]string{
			"cluster": applied, "gitops": state.Failed,
		}), "-gitops -cluster", ""},
		{"other mode refused", stateWith("cloud", map[string]string{"cluster": applied}), "", "cloud lab"},
	}
	g := catalogGraph(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := PlanDown(g, cfgFor("local", "standard"), tt.st, DownOptions{})
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
			want: "+registry +cluster +gitops +core +obs +chaos"},
		// sample-db's TLS (gitops/stacks/sample-db/templates/tls.yaml) needs
		// cert-manager from core: without it Argo CD cannot sync the app.
		{name: "sample-db pulls in core", stacks: []string{"sample-db"}, st: state.New(),
			want: "+registry +cluster +gitops +core +secrets +sample-db"},
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

func TestPlanUpRevision(t *testing.T) {
	applied := state.Applied
	upToCore := map[string]string{"registry": applied, "cluster": applied, "gitops": applied, "core": applied}
	onBranch := stateWith("local", upToCore)
	onBranch.Revision = "feat/obs"
	unrecorded := stateWith("local", upToCore)
	unrecorded.Revision = ""
	tests := []struct {
		name         string
		st           state.State
		stacks       []string
		revision     string
		want         string
		wantRevision string
		wantWarning  bool
		wantErr      string
	}{
		{name: "fresh lab tracks main", st: state.New(), stacks: []string{"core"},
			want: "+registry +cluster +gitops +core", wantRevision: "main"},
		{name: "fresh lab on a branch warns", st: state.New(), stacks: []string{"core"}, revision: "feat/obs",
			want: "+registry +cluster +gitops +core", wantRevision: "feat/obs", wantWarning: true},
		{name: "on main, plain up keeps gitops", st: stateWith("local", upToCore), stacks: []string{"core"},
			want: "=registry =cluster =gitops =core", wantRevision: "main"},
		{name: "on main, --revision re-applies gitops", st: stateWith("local", upToCore), stacks: []string{"core"}, revision: "feat/obs",
			want: "=registry =cluster +gitops =core", wantRevision: "feat/obs", wantWarning: true},
		{name: "on a branch, plain up moves back to main", st: onBranch, stacks: []string{"core"},
			want: "=registry =cluster +gitops =core", wantRevision: "main"},
		{name: "on a branch, same --revision keeps", st: onBranch, stacks: []string{"core"}, revision: "feat/obs",
			want: "=registry =cluster =gitops =core", wantRevision: "feat/obs", wantWarning: true},
		{name: "unrecorded revision is re-applied", st: unrecorded, stacks: []string{"core"},
			want: "=registry =cluster +gitops =core", wantRevision: "main"},
		{name: "no gitops in the selection, no revision", st: state.New(), stacks: []string{"cluster"},
			want: "+registry +cluster", wantRevision: ""},
		{name: "--revision without gitops is an error", st: state.New(), stacks: []string{"cluster"}, revision: "feat/obs",
			wantErr: "--revision applies to the gitops stack"},
	}
	g := catalogGraph(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := PlanUp(g, cfgFor("local", "minimal"), tt.st, PlanOptions{Stacks: tt.stacks, Revision: tt.revision})
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
			if p.Revision != tt.wantRevision {
				t.Errorf("revision = %q, want %q", p.Revision, tt.wantRevision)
			}
			warned := slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "a plain lab up moves it back") })
			if warned != tt.wantWarning {
				t.Errorf("warnings = %q, want revision warning %v", p.Warnings, tt.wantWarning)
			}
			for _, s := range p.Steps {
				if s.Stack == "gitops" && s.Action == Apply && tt.st.Stacks["gitops"].Status == applied &&
					!strings.HasPrefix(s.Reason, "revision ") {
					t.Errorf("gitops reason = %q", s.Reason)
				}
			}
		})
	}
}

// lab up --refresh-repo re-applies a deployed gitops stack, which reloads
// the repository Secret; a gitops stack not deployed yet is applied anyway.
func TestPlanUpRefreshRepo(t *testing.T) {
	applied := state.Applied
	upToCore := stateWith("local", map[string]string{"registry": applied, "cluster": applied, "gitops": applied, "core": applied})
	g := catalogGraph(t)
	tests := []struct {
		name       string
		st         state.State
		stacks     []string
		want       string
		wantReason string
		wantErr    string
	}{
		{name: "deployed gitops is re-applied", st: upToCore, stacks: []string{"gitops"},
			want: "=registry =cluster +gitops =core", wantReason: "reload the repository Secret from repo.sshKeyFile"},
		{name: "not deployed: applied as usual", st: state.New(), stacks: []string{"gitops"},
			want: "+registry +cluster +gitops", wantReason: "not deployed"},
		{name: "without gitops in the selection", st: upToCore, stacks: []string{"cluster"},
			wantErr: "--refresh-repo applies to the gitops stack"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := PlanUp(g, cfgFor("local", "minimal"), tt.st, PlanOptions{Stacks: tt.stacks, RefreshRepo: true})
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
			for _, s := range p.Steps {
				if s.Stack == "gitops" && s.Reason != tt.wantReason {
					t.Errorf("gitops reason = %q, want %q", s.Reason, tt.wantReason)
				}
			}
		})
	}
}

// A provider can ask for a deployed stack to be re-applied (lab-zot made
// with other host overrides); stacks not deployed apply as usual.
func TestPlanUpReapply(t *testing.T) {
	applied := state.Applied
	st := stateWith("local", map[string]string{"registry": applied, "cluster": applied})
	p, err := PlanUp(catalogGraph(t), cfgFor("local", "minimal"), st,
		PlanOptions{Stacks: []string{"cluster"}, Reapply: map[string]string{"registry": "lab-zot changed"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := render(p); got != "+registry =cluster" {
		t.Errorf("plan = %s", got)
	}
	if p.Steps[0].Reason != "lab-zot changed" {
		t.Errorf("reason = %q", p.Steps[0].Reason)
	}
}

// localSupports is what the local provider and the Argo CD runner run today.
func localSupports(s string) bool {
	return slices.Contains([]string{"registry", "cluster", "gitops", "core", "obs", "sample-app"}, s)
}

func TestPlanUpSupport(t *testing.T) {
	minimalObs := cfgFor("local", "minimal")
	minimalObs.ExtraStacks = []string{"obs"}
	allButSecrets := func(s string) bool { return s != "secrets" }
	tests := []struct {
		name         string
		cfg          config.Config
		supports     func(string) bool
		st           state.State
		want         string
		wantChanges  int
		wantWarnings []string
		reasons      map[string]string
	}{
		{
			name: "unsupported stacks are skipped with one warning", cfg: cfgFor("local", "standard"), supports: localSupports,
			st:           state.New(),
			want:         "+registry +cluster +gitops +core ~secrets +obs ~policy ~scaling +sample-app ~sample-db",
			wantChanges:  6,
			wantWarnings: []string{"4 stacks skipped, not supported by the local provider yet: secrets, policy, scaling, sample-db"},
			reasons:      map[string]string{"secrets": "not supported by the local provider yet"},
		},
		{
			name: "stacks that need a skipped stack are skipped too", cfg: cfgFor("local", "full"), supports: allButSecrets,
			st:           state.New(),
			want:         "+registry +cluster +gitops +core ~secrets +obs +policy +scaling ~ci +chaos ~ai +sample-app ~sample-db",
			wantChanges:  9,
			wantWarnings: []string{"4 stacks skipped, not supported by the local provider yet: secrets, ci, ai, sample-db"},
			reasons:      map[string]string{"ci": "needs secrets, which is skipped", "ai": "needs secrets, which is skipped", "sample-db": "needs secrets, which is skipped"},
		},
		{
			name: "minimal plus obs is all supported", cfg: minimalObs, supports: localSupports,
			st:          state.New(),
			want:        "+registry +cluster +gitops +core +sample-app +obs",
			wantChanges: 6,
		},
		{
			name: "a deployed stack is never skipped", cfg: cfgFor("local", "standard"), supports: localSupports,
			st: stateWith("local", map[string]string{"registry": state.Applied, "cluster": state.Applied, "gitops": state.Applied,
				"core": state.Applied, "secrets": state.Applied, "obs": state.Applied, "sample-app": state.Applied}),
			want:         "=registry =cluster =gitops =core =secrets =obs ~policy ~scaling =sample-app ~sample-db",
			wantWarnings: []string{"3 stacks skipped, not supported by the local provider yet: policy, scaling, sample-db"},
		},
		{
			name: "nil supports skips nothing", cfg: cfgFor("local", "standard"),
			st:          state.New(),
			want:        "+registry +cluster +gitops +core +secrets +obs +policy +scaling +sample-app +sample-db",
			wantChanges: 10,
		},
	}
	g := catalogGraph(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := PlanUp(g, tt.cfg, tt.st, PlanOptions{Supports: tt.supports})
			if err != nil {
				t.Fatal(err)
			}
			if got := render(p); got != tt.want {
				t.Errorf("plan = %s\nwant   %s", got, tt.want)
			}
			if p.Changes() != tt.wantChanges || p.Count(Apply) != tt.wantChanges {
				t.Errorf("changes = %d, applies = %d, want %d", p.Changes(), p.Count(Apply), tt.wantChanges)
			}
			if !slices.Equal(p.Warnings, tt.wantWarnings) {
				t.Errorf("warnings = %q\nwant       %q", p.Warnings, tt.wantWarnings)
			}
			for _, s := range p.Steps {
				if want, ok := tt.reasons[s.Stack]; ok && s.Reason != want {
					t.Errorf("%s reason %q, want %q", s.Stack, s.Reason, want)
				}
			}
		})
	}
}

func TestProfileNameSkipsStacksInTheProfile(t *testing.T) {
	cfg := cfgFor("local", "standard")
	cfg.ExtraStacks = []string{"obs", "ci", "ci"}
	if got := ProfileName(cfg); got != "standard + ci" {
		t.Errorf("ProfileName = %q, want %q", got, "standard + ci")
	}
}

func TestPlanProfileName(t *testing.T) {
	for _, tt := range []struct {
		extra []string
		want  string
	}{{nil, "minimal"}, {[]string{"obs"}, "minimal + obs"}, {[]string{"obs", "policy"}, "minimal + obs, policy"}} {
		if got := (Plan{Profile: "minimal", Extra: tt.extra}).ProfileName(); got != tt.want {
			t.Errorf("ProfileName(%v) = %q, want %q", tt.extra, got, tt.want)
		}
	}
}

func TestPlanDownStacks(t *testing.T) {
	applied := state.Applied
	lab := stateWith("local", map[string]string{
		"registry": applied, "cluster": applied, "gitops": applied, "core": applied, "obs": applied, "sample-app": state.Failed,
	})
	tests := []struct {
		name    string
		stacks  []string
		want    string
		reasons []string
		wantErr string
	}{
		{name: "one leaf stack", stacks: []string{"sample-app"}, want: "-sample-app", reasons: []string{"selected"}},
		{name: "deployed dependents go first", stacks: []string{"core"}, want: "-sample-app -obs -core",
			reasons: []string{"needs core", "needs core", "selected"}},
		{name: "a stack that is not deployed is nothing to do", stacks: []string{"policy"}, want: ""},
		{name: "unknown stack", stacks: []string{"harbor"}, wantErr: `unknown stack "harbor"`},
	}
	g := catalogGraph(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := PlanDown(g, cfgFor("local", "minimal"), lab, DownOptions{Stacks: tt.stacks})
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
			var reasons []string
			for _, s := range p.Steps {
				reasons = append(reasons, s.Reason)
			}
			if !slices.Equal(reasons, tt.reasons) {
				t.Errorf("reasons = %q, want %q", reasons, tt.reasons)
			}
		})
	}
}
