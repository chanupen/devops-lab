package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGitOpsRevision(t *testing.T) {
	for _, tt := range []struct {
		name     string
		state    GitOpsState
		err      error
		want     Status
		wantFix  string
		wantText string
	}{
		{"deleted branch", GitOpsState{Revision: "feat/old", Problems: []string{"ComparisonError: unable to resolve 'feat/old' to a commit SHA"}}, nil, Fail, "lab up", "feat/old"},
		{"other comparison error", GitOpsState{Revision: "feat/old", Problems: []string{"ComparisonError: permission denied"}}, nil, Warn, "lab status", "permission denied"},
		{"main unresolved", GitOpsState{Revision: "main", Problems: []string{"ComparisonError: unable to resolve 'main' to a commit SHA"}}, nil, Warn, "lab status", "main"},
		{"cannot read", GitOpsState{}, errors.New("unavailable"), Warn, "lab status", "unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sys := System{GitOps: func(context.Context) (GitOpsState, error) { return tt.state, tt.err }}
			r := checkGitOpsRevision(context.Background(), sys, Options{Stacks: []string{"gitops"}})
			if r.Status != tt.want || !strings.Contains(r.Fix, tt.wantFix) || !strings.Contains(r.Detail, tt.wantText) {
				t.Fatalf("got %+v", r)
			}
		})
	}
}
