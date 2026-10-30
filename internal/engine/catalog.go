package engine

import (
	"fmt"
	"slices"
)

// Catalog returns the lab's stacks. Mode-specific details (KEDA or Karpenter,
// Zot or ECR) live in providers and gitops overlays, not here.
func Catalog() []Stack {
	return []Stack{
		{Name: "core"}, // cluster, CNI, metrics-server, Gateway API, cert-manager
		{Name: "gitops", DependsOn: []string{"core"}},
		{Name: "registry", DependsOn: []string{"core"}},
		{Name: "secrets", DependsOn: []string{"gitops"}},
		{Name: "obs", DependsOn: []string{"gitops"}},
		{Name: "policy", DependsOn: []string{"gitops"}},
		{Name: "scaling", DependsOn: []string{"gitops", "obs"}},
		{Name: "ci", DependsOn: []string{"gitops", "registry", "secrets"}},
		{Name: "chaos", DependsOn: []string{"obs"}},
		{Name: "ai", DependsOn: []string{"obs", "secrets"}},
		{Name: "sample-app", DependsOn: []string{"gitops", "registry"}},
	}
}

// profileStacks lists the stacks each memory profile asks for; Order adds
// their dependencies.
var profileStacks = map[string][]string{
	"minimal":  {"core", "gitops", "registry", "sample-app"},
	"standard": {"core", "gitops", "registry", "secrets", "obs", "policy", "scaling", "sample-app"},
	"full":     {"core", "gitops", "registry", "secrets", "obs", "policy", "scaling", "ci", "chaos", "ai", "sample-app"},
}

// ProfileStacks returns the stacks a profile asks for.
func ProfileStacks(profile string) ([]string, error) {
	s, ok := profileStacks[profile]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q", profile)
	}
	return slices.Clone(s), nil
}
