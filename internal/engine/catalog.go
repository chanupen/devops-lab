package engine

import (
	"fmt"
	"slices"
)

// Catalog returns the lab's stacks. Mode-specific details (KEDA or Karpenter,
// Zot or ECR) live in providers and gitops overlays, not here.
//
// LocalStack is not a stack: the local provider starts it from Secrets() and
// ObjectStore() when a stack such as secrets needs those APIs.
func Catalog() []Stack {
	return []Stack{
		// Provider work: kind or EKS, CNI, StorageClass.
		{Name: "cluster"},
		// Argo CD (or Flux) via the Helm SDK. Every stack after this is a GitOps Application.
		{Name: "gitops", DependsOn: []string{"cluster"}},
		// metrics-server, Envoy Gateway (Gateway API), cert-manager as Argo CD apps.
		{Name: "core", DependsOn: []string{"gitops"}},
		// Zot locally (also a pull-through cache, so it comes up early), ECR in cloud.
		{Name: "registry", DependsOn: []string{"cluster"}},
		{Name: "secrets", DependsOn: []string{"gitops"}},
		{Name: "obs", DependsOn: []string{"gitops"}},
		{Name: "policy", DependsOn: []string{"gitops"}},
		{Name: "scaling", DependsOn: []string{"gitops", "obs"}},
		{Name: "ci", DependsOn: []string{"gitops", "registry", "secrets"}},
		{Name: "chaos", DependsOn: []string{"obs"}},
		{Name: "ai", DependsOn: []string{"obs", "secrets"}},
		// Needs the Gateway from core. Its ServiceMonitor is applied only when obs
		// is present (gitops overlay), so minimal stays small.
		{Name: "sample-app", DependsOn: []string{"core", "registry"}},
	}
}

// profileStacks lists the stacks each memory profile asks for; Order adds
// their dependencies.
var profileStacks = map[string][]string{
	"minimal":  {"cluster", "gitops", "core", "registry", "sample-app"},
	"standard": {"cluster", "gitops", "core", "registry", "secrets", "obs", "policy", "scaling", "sample-app"},
	"full":     {"cluster", "gitops", "core", "registry", "secrets", "obs", "policy", "scaling", "ci", "chaos", "ai", "sample-app"},
}

// ProfileStacks returns the stacks a profile asks for.
func ProfileStacks(profile string) ([]string, error) {
	s, ok := profileStacks[profile]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q", profile)
	}
	return slices.Clone(s), nil
}
