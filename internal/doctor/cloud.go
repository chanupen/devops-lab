package doctor

import (
	"context"
	"strings"
)

// CloudProbe answers the cloud checks. cmd/lab builds it from the lab
// profile's session once, before the checks run, so an MFA prompt happens
// at most once and never inside a concurrent check.
type CloudProbe struct {
	// SessionErr is why no session could be made (for example no MFA code
	// in a non-interactive run); the AWS checks fail or skip with it.
	SessionErr   error
	Profile      string
	Identity     func(ctx context.Context) (arn, account string, err error)
	BucketExists func(ctx context.Context, account string) (bool, error)
	BudgetExists func(ctx context.Context, account string) (bool, error)
	BucketName   func(account string) string
	BudgetName   string
	// K8sVersion is lab.yaml's pin; EKSVersion checks it is the newest EKS
	// version in standard support and returns that version.
	K8sVersion string
	EKSVersion func(ctx context.Context) (newest string, err error)
	// ReaperReady is nil when the reaper deletes for real and ran cleanly in
	// the last 30 minutes: the dead man's switch lab up --mode cloud needs.
	ReaperReady func(ctx context.Context, account string) error
}

// CloudChecks returns the checks for lab doctor --mode cloud.
func CloudChecks(p CloudProbe) []Check {
	return []Check{
		{"tofu", toolCheck("tofu")},
		{"aws", toolCheck("aws")},
		{"lab profile", func(ctx context.Context, _ System, _ Options) Result { return checkLabProfile(ctx, p) }},
		{"state bucket", func(ctx context.Context, _ System, _ Options) Result { return checkStateBucket(ctx, p) }},
		{"budget", func(ctx context.Context, _ System, _ Options) Result { return checkBudget(ctx, p) }},
		{"eks version", func(ctx context.Context, _ System, _ Options) Result { return checkEKSVersion(ctx, p) }},
		{"ttl reaper", func(ctx context.Context, _ System, _ Options) Result { return checkReaper(ctx, p) }},
	}
}

func checkEKSVersion(ctx context.Context, p CloudProbe) Result {
	if p.SessionErr != nil || p.EKSVersion == nil {
		return Result{Status: Skip, Detail: "needs the " + p.Profile + " profile"}
	}
	newest, err := p.EKSVersion(ctx)
	if err != nil {
		return fail("set k8sVersion in lab.yaml to the newest EKS version in standard support", "%v", err)
	}
	return ok("Kubernetes %s is the newest EKS version in standard support", newest)
}

func checkReaper(ctx context.Context, p CloudProbe) Result {
	if p.SessionErr != nil || p.ReaperReady == nil {
		return Result{Status: Skip, Detail: "needs the " + p.Profile + " profile"}
	}
	_, account, err := p.Identity(ctx)
	if err != nil {
		return Result{Status: Skip, Detail: "needs the " + p.Profile + " profile"}
	}
	if err := p.ReaperReady(ctx, account); err != nil {
		return fail("lab up --mode cloud refuses until this passes", "%v", err)
	}
	return ok("reaper deletes for real and ran cleanly in the last 30 minutes")
}

func checkLabProfile(ctx context.Context, p CloudProbe) Result {
	if p.SessionErr != nil {
		return fail("lab cloud login (asks for the MFA code), or lab cloud baseline apply --profile devops if lab-admin does not exist yet",
			"profile %s: %v", p.Profile, p.SessionErr)
	}
	arn, _, err := p.Identity(ctx)
	if err != nil {
		return fail("", "profile %s: %v", p.Profile, err)
	}
	if !strings.Contains(arn, ":assumed-role/lab-admin/") {
		return fail("set role_arn to lab-admin in ~/.aws/config [profile "+p.Profile+"]", "profile %s is %s, not lab-admin", p.Profile, arn)
	}
	return ok("profile %s assumes lab-admin", p.Profile)
}

func checkStateBucket(ctx context.Context, p CloudProbe) Result {
	return checkExists(ctx, p, "state bucket", p.BucketExists, p.BucketName)
}

func checkBudget(ctx context.Context, p CloudProbe) Result {
	return checkExists(ctx, p, "budget", p.BudgetExists, func(string) string { return p.BudgetName })
}

func checkExists(ctx context.Context, p CloudProbe, what string, exists func(context.Context, string) (bool, error), name func(string) string) Result {
	if p.SessionErr != nil {
		return Result{Status: Skip, Detail: "needs the " + p.Profile + " profile"}
	}
	_, account, err := p.Identity(ctx)
	if err != nil {
		return Result{Status: Skip, Detail: "needs the " + p.Profile + " profile"}
	}
	found, err := exists(ctx, account)
	switch {
	case err != nil:
		return fail("", "%s %s: %v", what, name(account), err)
	case !found:
		return fail("lab cloud baseline apply", "%s %s does not exist", what, name(account))
	}
	return ok("%s %s", what, name(account))
}
