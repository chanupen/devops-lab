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
	if got := strings.Join(names, ","); got != "tofu,aws,lab profile,state bucket,budget,eks version,ttl reaper,teardown build" {
		t.Errorf("checks = %s", got)
	}
}

func TestCloudFixInspections(t *testing.T) {
	p := probe("arn:aws:sts::111122223333:assumed-role/lab-admin/lab", false, false)
	p.EKSVersion = func(context.Context) (string, []string, error) {
		return "", nil, errors.New("unsupported version")
	}
	p.ReaperReady = func(context.Context, string) error { return errors.New("stale heartbeat") }
	p.TeardownBuild = func(context.Context) (string, error) { return "fixture-running-build", nil }
	for _, tt := range []struct {
		name, command string
	}{
		{"state bucket", "aws s3api list-buckets --profile lab"},
		{"budget", "aws sts get-caller-identity --profile lab"},
		{"eks version", "aws sts get-caller-identity --profile lab"},
		{"ttl reaper", "aws sts get-caller-identity --profile lab"},
		{"teardown build", "aws sts get-caller-identity --profile lab"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, c := range CloudChecks(p) {
				if c.Name != tt.name {
					continue
				}
				results := Run(context.Background(), System{}, Options{}, []Check{c})
				commands := FixCommands(results, Options{})
				if !Failed(results) || len(commands) != 1 || commands[0].Command != tt.command ||
					!commands[0].Manual || commands[0].Effect != "read-only" || commands[0].RequiresSudo {
					t.Fatalf("results=%+v commands=%+v", results, commands)
				}
				return
			}
			t.Fatal("missing check")
		})
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

func TestEKSVersionAndReaperChecks(t *testing.T) {
	const admin = "arn:aws:sts::111122223333:assumed-role/lab-admin/lab"
	ctx := context.Background()
	p := probe(admin, true, true)
	if r := checkEKSVersion(ctx, p); r.Status != Skip {
		t.Errorf("no probe: %s", r.Status)
	}
	p.EKSVersion = func(context.Context) (string, []string, error) {
		return "Kubernetes 1.36: the newest version in standard support that Karpenter, Argo CD all support", nil, nil
	}
	p.ReaperReady = func(context.Context, string) error { return nil }
	if r := checkEKSVersion(ctx, p); r.Status != OK || !strings.Contains(r.Detail, "1.36: the newest") {
		t.Errorf("eks ok: %s %q", r.Status, r.Detail)
	}
	if r := checkReaper(ctx, p); r.Status != OK {
		t.Errorf("reaper ok: %s %q", r.Status, r.Detail)
	}
	p.EKSVersion = func(context.Context) (string, []string, error) {
		return "Kubernetes 1.37: pinned by cloud.k8sVersion", []string{"Kubernetes 1.37 is outside the stated range of Argo CD"}, nil
	}
	if r := checkEKSVersion(ctx, p); r.Status != Warn || !strings.Contains(r.Detail, "Argo CD") {
		t.Errorf("eks warning: %s %q", r.Status, r.Detail)
	}
	p.EKSVersion = func(context.Context) (string, []string, error) {
		return "", nil, errors.New("cloud.k8sVersion 1.32 is not in EKS standard support (standard: 1.34, 1.35, 1.36, 1.37)")
	}
	p.ReaperReady = func(context.Context, string) error { return errors.New("the TTL reaper is in dry run") }
	if r := checkEKSVersion(ctx, p); r.Status != Fail || !strings.Contains(r.Detail, "1.32 is not in EKS standard support") {
		t.Errorf("eks old: %s %q", r.Status, r.Detail)
	}
	if r := checkReaper(ctx, p); r.Status != Fail || !strings.Contains(r.Detail, "dry run") || !strings.Contains(r.Fix, "refuses") {
		t.Errorf("reaper dry: %s %q %q", r.Status, r.Detail, r.Fix)
	}
}

// lab up --mode cloud refuses while a teardown build runs (it raced a new
// lab on 2 Nov 2026, build cd95d461); doctor shows that build as a failure.
func TestTeardownBuildCheck(t *testing.T) {
	ctx := context.Background()
	p := probe("arn:aws:sts::111122223333:assumed-role/lab-admin/lab", true, true)
	if r := checkTeardownBuild(ctx, p); r.Status != Skip {
		t.Errorf("no probe: %s", r.Status)
	}
	p.TeardownBuild = func(context.Context) (string, error) { return "", nil }
	if r := checkTeardownBuild(ctx, p); r.Status != OK {
		t.Errorf("idle: %s %q", r.Status, r.Detail)
	}
	p.TeardownBuild = func(context.Context) (string, error) { return "lab-teardown:cd95d461-ac12-49b6-875c-140e383b16ee", nil }
	if r := checkTeardownBuild(ctx, p); r.Status != Fail || !strings.Contains(r.Detail, "lab-teardown:cd95d461-ac12-49b6-875c-140e383b16ee") ||
		!strings.Contains(r.Fix, "refuses") {
		t.Errorf("running: %s %q %q", r.Status, r.Detail, r.Fix)
	}
	p.TeardownBuild = func(context.Context) (string, error) { return "", errors.New("AccessDeniedException") }
	if r := checkTeardownBuild(ctx, p); r.Status != Fail {
		t.Errorf("error: %s %q", r.Status, r.Detail)
	}
}
