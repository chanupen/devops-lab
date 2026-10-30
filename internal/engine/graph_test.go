package engine

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestNewGraph(t *testing.T) {
	tests := []struct {
		name      string
		stacks    []Stack
		wantErr   string
		wantCycle []string
	}{
		{name: "ok", stacks: []Stack{{Name: "a"}, {Name: "b", DependsOn: []string{"a"}}}},
		{name: "empty name", stacks: []Stack{{Name: ""}}, wantErr: "empty name"},
		{name: "duplicate", stacks: []Stack{{Name: "a"}, {Name: "a"}}, wantErr: `"a" defined twice`},
		{name: "unknown dependency", stacks: []Stack{{Name: "a", DependsOn: []string{"ghost"}}}, wantErr: `unknown stack "ghost"`},
		{name: "self cycle", stacks: []Stack{{Name: "a", DependsOn: []string{"a"}}}, wantCycle: []string{"a", "a"}},
		{name: "two-node cycle", stacks: []Stack{
			{Name: "a", DependsOn: []string{"b"}},
			{Name: "b", DependsOn: []string{"a"}},
		}, wantCycle: []string{"a", "b", "a"}},
		{name: "cycle behind a root", stacks: []Stack{
			{Name: "root"},
			{Name: "x", DependsOn: []string{"root", "y"}},
			{Name: "y", DependsOn: []string{"z"}},
			{Name: "z", DependsOn: []string{"x"}},
		}, wantCycle: []string{"x", "y", "z", "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewGraph(tt.stacks)
			switch {
			case tt.wantCycle != nil:
				var ce *CycleError
				if !errors.As(err, &ce) {
					t.Fatalf("err = %v, want CycleError", err)
				}
				if !slices.Equal(ce.Path, tt.wantCycle) {
					t.Errorf("cycle = %v, want %v", ce.Path, tt.wantCycle)
				}
				if !strings.Contains(err.Error(), strings.Join(tt.wantCycle, " → ")) {
					t.Errorf("message %q lacks the path", err)
				}
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestOrder(t *testing.T) {
	g, err := NewGraph([]Stack{
		{Name: "core"},
		{Name: "gitops", DependsOn: []string{"core"}},
		{Name: "registry", DependsOn: []string{"core"}},
		{Name: "obs", DependsOn: []string{"gitops"}},
		{Name: "app", DependsOn: []string{"registry", "obs"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		in      []string
		want    []string
		wantErr string
	}{
		{"leaf pulls in its chain", []string{"obs"}, []string{"core", "gitops", "obs"}, ""},
		{"diamond, dependencies once", []string{"app"}, []string{"core", "registry", "gitops", "obs", "app"}, ""},
		{"duplicates ignored", []string{"core", "core"}, []string{"core"}, ""},
		{"request order breaks ties", []string{"registry", "gitops"}, []string{"core", "registry", "gitops"}, ""},
		{"nothing requested", nil, nil, ""},
		{"unknown", []string{"nope"}, nil, `unknown stack "nope"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := g.Order(tt.in...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Order(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// Every stack in every profile must come after all of its dependencies.
func TestCatalogProfiles(t *testing.T) {
	g, err := NewGraph(Catalog())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	deps := map[string][]string{}
	for _, s := range Catalog() {
		deps[s.Name] = s.DependsOn
	}
	for _, profile := range []string{"minimal", "standard", "full"} {
		t.Run(profile, func(t *testing.T) {
			want, err := ProfileStacks(profile)
			if err != nil {
				t.Fatal(err)
			}
			order, err := g.Order(want...)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(order, dedupe(order)) || len(order) != len(want) {
				t.Errorf("profile %s is not closed under dependencies: asked %v, ordered %v", profile, want, order)
			}
			pos := map[string]int{}
			for i, s := range order {
				pos[s] = i
			}
			for _, s := range order {
				for _, d := range deps[s] {
					if pos[d] >= pos[s] {
						t.Errorf("%s at %d comes before its dependency %s at %d", s, pos[s], d, pos[d])
					}
				}
			}
		})
	}
	if _, err := ProfileStacks("huge"); err == nil {
		t.Error("unknown profile accepted")
	}
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// Edges settled in the PR #2 review: the tool bootstraps only cluster and
// gitops; core add-ons are GitOps apps; sample-app needs core and registry.
// PR #3 put registry first so Zot is up before the kind nodes start.
func TestCatalogShape(t *testing.T) {
	g := catalogGraph(t)
	deps := map[string][]string{}
	for _, s := range Catalog() {
		deps[s.Name] = s.DependsOn
	}
	tests := []struct {
		stack     string
		wantDeps  []string // direct
		wantChain []string // Order(stack)
	}{
		{"registry", nil, []string{"registry"}},
		{"cluster", []string{"registry"}, []string{"registry", "cluster"}},
		{"gitops", []string{"cluster"}, []string{"registry", "cluster", "gitops"}},
		{"core", []string{"gitops"}, []string{"registry", "cluster", "gitops", "core"}},
		{"sample-app", []string{"core", "registry"}, []string{"registry", "cluster", "gitops", "core", "sample-app"}},
	}
	for _, tt := range tests {
		t.Run(tt.stack, func(t *testing.T) {
			if !slices.Equal(deps[tt.stack], tt.wantDeps) {
				t.Errorf("DependsOn = %v, want %v", deps[tt.stack], tt.wantDeps)
			}
			chain, err := g.Order(tt.stack)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(chain, tt.wantChain) {
				t.Errorf("Order = %v, want %v", chain, tt.wantChain)
			}
		})
	}

	// GitOps after bootstrap: apart from provider work (registry, cluster),
	// every stack is reached through gitops.
	for _, name := range g.Names() {
		if name == "cluster" || name == "gitops" || name == "registry" {
			continue
		}
		chain, _ := g.Order(name)
		if !slices.Contains(chain, "gitops") {
			t.Errorf("%s does not depend on gitops", name)
		}
	}
	// LocalStack is started by the local provider, never planned as a stack.
	if g.Has("localstack") {
		t.Error("localstack must not be a stack")
	}
}
