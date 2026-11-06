package aws

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	tfjson "github.com/hashicorp/terraform-json"
)

// managedPolicyARN matches an AWS managed policy in any partition. Policies
// in the account (iam::<account>:policy/...) are created by the plan
// itself, so they are not checked.
var managedPolicyARN = regexp.MustCompile(`^arn:aws[a-z-]*:iam::aws:policy/\S+$`)

// policyAttrs are the resource attributes that name IAM policies:
// aws_iam_*_policy_attachment (policy_arn), aws_iam_role (managed_policy_arns)
// and aws_iam_role_policy_attachments_exclusive (policy_arns).
var policyAttrs = []string{"policy_arn", "managed_policy_arns", "policy_arns"}

// PlanManagedPolicies lists, sorted and once each, the AWS managed policies
// a saved plan attaches. Values come from the plan, so names built from
// variables, for_each and child modules (Karpenter) are included. Values
// tofu only knows after apply cannot be checked and are skipped.
func PlanManagedPolicies(p *tfjson.Plan) []string {
	if p == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, rc := range p.ResourceChanges {
		if rc.Change == nil || rc.Change.Actions.Delete() || rc.Change.Actions.NoOp() {
			continue
		}
		after, ok := rc.Change.After.(map[string]any)
		if !ok {
			continue
		}
		for _, attr := range policyAttrs {
			for _, v := range strs(after[attr]) {
				if managedPolicyARN.MatchString(v) {
					seen[v] = true
				}
			}
		}
	}
	arns := make([]string, 0, len(seen))
	for a := range seen {
		arns = append(arns, a)
	}
	slices.Sort(arns)
	return arns
}

// strs reads a string or a list of strings from plan JSON.
func strs(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// PolicyGetter is the IAM call CheckManagedPolicies needs.
type PolicyGetter interface {
	GetPolicy(ctx context.Context, in *iam.GetPolicyInput, opts ...func(*iam.Options)) (*iam.GetPolicyOutput, error)
}

// CheckManagedPolicies looks up every policy with iam:GetPolicy and fails,
// naming each one, if any does not exist. A typo in a policy name then
// stops lab before tofu apply instead of minutes into it, with half a
// cluster built.
func CheckManagedPolicies(ctx context.Context, c PolicyGetter, arns []string) error {
	var missing []string
	for _, a := range arns {
		_, err := c.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(a)})
		var nse *iamtypes.NoSuchEntityException
		switch {
		case errors.As(err, &nse):
			missing = append(missing, a)
		case err != nil:
			return fmt.Errorf("check AWS managed policy %s: %w", a, err)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the plan attaches AWS managed policies that do not exist (check the names in infra/terraform/aws): %s",
			strings.Join(missing, ", "))
	}
	return nil
}
