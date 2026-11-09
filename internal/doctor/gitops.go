package doctor

import (
	"context"
	"slices"
	"strings"
)

// GitOpsState is the root Argo CD Application's revision and error conditions.
type GitOpsState struct {
	Revision string
	Problems []string
}

func checkGitOpsRevision(ctx context.Context, sys System, opts Options) Result {
	if !slices.Contains(opts.Stacks, "gitops") || sys.GitOps == nil {
		return Result{Status: Skip, Detail: "gitops not deployed or unavailable"}
	}
	if opts.DeferClusterHealth {
		return Result{Status: Skip, Detail: "cluster health unverified until stopped lab containers restart"}
	}
	st, err := sys.GitOps(ctx)
	if err != nil {
		return warn("lab status; argocd app get lab-bootstrap", "cannot read gitops revision: %s", firstLine(err.Error()))
	}
	if st.Revision == "" {
		return warn("lab status; argocd app get lab-bootstrap", "gitops revision is missing")
	}
	expected := "unable to resolve '" + st.Revision + "' to a commit SHA"
	for _, problem := range st.Problems {
		if st.Revision != "main" && strings.HasPrefix(problem, "ComparisonError:") && strings.Contains(problem, expected) {
			return fail("lab up (moves gitops back to main)", "gitops revision %q cannot be resolved", st.Revision)
		}
	}
	for _, problem := range st.Problems {
		if strings.HasPrefix(problem, "ComparisonError:") {
			return warn("lab status; argocd app get lab-bootstrap", "gitops comparison failed: %s", firstLine(problem))
		}
	}
	return ok("revision %s (no comparison error)", st.Revision)
}
