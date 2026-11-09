package doctor

import (
	"context"
	"errors"
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
	// EKSVersion picks the cloud cluster's Kubernetes version (standard
	// support only, preferring the newest the pinned components support)
	// and says which and why; warnings do not fail the check.
	EKSVersion func(ctx context.Context) (choice string, warnings []string, err error)
	// ReaperReady is nil when the reaper deletes for real and ran cleanly in
	// the last 30 minutes: the dead man's switch lab up --mode cloud needs.
	ReaperReady func(ctx context.Context, account string) error
	// TeardownBuild is the ID of a lab-teardown build in progress, or "".
	// lab up --mode cloud refuses while one runs.
	TeardownBuild func(ctx context.Context) (string, error)
}

// CloudChecks returns the checks for lab doctor --mode cloud.
func CloudChecks(p CloudProbe) []Check {
	checks := []Check{
		{"tofu", toolCheck("tofu")},
		{"aws", toolCheck("aws")},
		{"lab profile", func(ctx context.Context, _ System, _ Options) Result { return checkLabProfile(ctx, p) }},
		{"state bucket", func(ctx context.Context, _ System, _ Options) Result { return checkStateBucket(ctx, p) }},
		{"budget", func(ctx context.Context, _ System, _ Options) Result { return checkBudget(ctx, p) }},
		{"eks version", func(ctx context.Context, _ System, _ Options) Result { return checkEKSVersion(ctx, p) }},
		{"ttl reaper", func(ctx context.Context, _ System, _ Options) Result { return checkReaper(ctx, p) }},
		{"teardown build", func(ctx context.Context, _ System, _ Options) Result { return checkTeardownBuild(ctx, p) }},
	}
	for i := range checks {
		var command, explanation string
		switch checks[i].Name {
		case "lab profile":
			command = "aws configure list --profile " + ShellArg(p.Profile)
			explanation = "Review the role/source/MFA profile configuration manually; never print credentials."
		case "state bucket":
			command = "aws s3api list-buckets --profile " + ShellArg(p.Profile)
			explanation = "Review baseline state bucket readiness; baseline apply is a billed cloud change requiring approval."
		case "budget":
			command = "aws sts get-caller-identity --profile " + ShellArg(p.Profile)
			explanation = "Confirm the account and review its baseline budget; baseline apply requires cloud approval."
		case "eks version", "ttl reaper", "teardown build":
			command = "aws sts get-caller-identity --profile " + ShellArg(p.Profile)
			explanation = "Review the reported cloud safety failure with the operator; do not bypass teardown, TTL or version gates."
		default:
			continue
		}
		checks[i].Run = inspect(checks[i].Run, inspection(command, explanation))
	}
	return checks
}

func checkTeardownBuild(ctx context.Context, p CloudProbe) Result {
	if p.SessionErr != nil || p.TeardownBuild == nil {
		return Result{Status: Skip, Detail: "needs the " + p.Profile + " profile"}
	}
	id, err := p.TeardownBuild(ctx)
	switch {
	case err != nil:
		return fail("", "%v", err)
	case id != "":
		return fail("lab up --mode cloud refuses until it finishes; follow it in /aws/codebuild/lab-teardown",
			"teardown build %s is running", id)
	}
	return ok("no teardown build running")
}

func checkEKSVersion(ctx context.Context, p CloudProbe) Result {
	if p.SessionErr != nil || p.EKSVersion == nil {
		return Result{Status: Skip, Detail: "needs the " + p.Profile + " profile"}
	}
	choice, warnings, err := p.EKSVersion(ctx)
	switch {
	case err != nil:
		return fail("the cloud lab runs only on EKS standard support: fix or remove cloud.k8sVersion in lab.yaml", "%v", err)
	case len(warnings) > 0:
		return warn("", "%s; %s", choice, strings.Join(warnings, "; "))
	}
	return ok("%s", choice)
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
	return ok("reaper deletes for real, ran cleanly and passed its self-test in the last 30 minutes")
}

func checkLabProfile(ctx context.Context, p CloudProbe) Result {
	var fixer interface{ Fix() string } // a profile that does not resolve says what to check
	if errors.As(p.SessionErr, &fixer) {
		return fail(fixer.Fix(), "%v", p.SessionErr)
	}
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
