package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func probe(arn string, bucket, budget bool) CloudProbe {
	return CloudProbe{
		Profile:      "lab",
		Identity:     func(context.Context) (string, string, error) { return arn, "111122223333", nil },
		BucketExists: func(context.Context, string) (bool, error) { return bucket, nil },
		BudgetExists: func(context.Context, string) (bool, error) { return budget, nil },
		BucketName:   func(a string) string { return "lab-tofu-state-" + a },
		BudgetName:   "lab-monthly",
	}
}

func TestCloudChecks(t *testing.T) {
	const admin = "arn:aws:sts::111122223333:assumed-role/lab-admin/lab"
	noSession := probe("", false, false)
	noSession.SessionErr = errors.New("this profile needs an MFA code: run lab cloud login first")
	tests := []struct {
		name    string
		p       CloudProbe
		profile Status
		bucket  Status
		budget  Status
		detail  string
	}{
		{"baseline in place", probe(admin, true, true), OK, OK, OK, "lab-tofu-state-111122223333"},
		{"profile is the plain user", probe("arn:aws:iam::111122223333:user/devops", true, true), Fail, OK, OK, "not lab-admin"},
		{"before the first apply", probe(admin, false, false), OK, Fail, Fail, "does not exist"},
		{"no MFA session", noSession, Fail, Skip, Skip, "lab cloud login"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got := []Result{checkLabProfile(ctx, tt.p), checkStateBucket(ctx, tt.p), checkBudget(ctx, tt.p)}
			for i, want := range []Status{tt.profile, tt.bucket, tt.budget} {
				if got[i].Status != want {
					t.Errorf("check %d = %s %q, want %s", i, got[i].Status, got[i].Detail, want)
				}
			}
			all := got[0].Detail + got[0].Fix + got[1].Detail + got[1].Fix
			if !strings.Contains(all, tt.detail) {
				t.Errorf("details %q lack %q", all, tt.detail)
			}
		})
	}
}

func TestCloudChecksList(t *testing.T) {
	var names []string
	for _, c := range CloudChecks(probe("", true, true)) {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "tofu,aws,lab profile,state bucket,budget" {
		t.Errorf("checks = %s", got)
	}
}

func TestTofuNeedsNativeLocking(t *testing.T) {
	sys := System{LookPath: func(string) (string, error) { return "/x/tofu", nil },
		Run: func(context.Context, string, ...string) (string, error) {
			return "OpenTofu v1.9.1\non linux_amd64", nil
		}}
	if r := runTool(context.Background(), sys, "tofu"); r.Status != Warn || !strings.Contains(r.Detail, "want ≥ 1.10.0") {
		t.Errorf("tofu 1.9.1: %s %q, want a warning for < 1.10 (no use_lockfile)", r.Status, r.Detail)
	}
}
