package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	tfjson "github.com/hashicorp/terraform-json"
)

// planJSON is a trimmed tofu show -json: a for_each attachment, one in a
// child module, a typo, an account policy, a delete and a value known only
// after apply.
const planJSON = `{
  "format_version": "1.2",
  "resource_changes": [
    {"address": "aws_iam_role_policy_attachment.cluster[\"AmazonEKSClusterPolicy\"]", "type": "aws_iam_role_policy_attachment",
     "change": {"actions": ["create"], "after": {"policy_arn": "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"}}},
    {"address": "aws_iam_role_policy_attachment.ebs_csi", "type": "aws_iam_role_policy_attachment",
     "change": {"actions": ["create"], "after": {"policy_arn": "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicyV2"}}},
    {"address": "module.karpenter.aws_iam_role_policy_attachment.node[\"x\"]", "type": "aws_iam_role_policy_attachment",
     "change": {"actions": ["create"], "after": {"policy_arn": "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"}}},
    {"address": "aws_iam_role.exclusive", "type": "aws_iam_role_policy_attachments_exclusive",
     "change": {"actions": ["update"], "after": {"policy_arns": ["arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore", "arn:aws:iam::111111111111:policy/lab-own"]}}},
    {"address": "aws_iam_role_policy_attachment.old", "type": "aws_iam_role_policy_attachment",
     "change": {"actions": ["delete"], "after": null, "before": {"policy_arn": "arn:aws:iam::aws:policy/Gone"}}},
    {"address": "aws_iam_role_policy_attachment.later", "type": "aws_iam_role_policy_attachment",
     "change": {"actions": ["create"], "after": {}, "after_unknown": {"policy_arn": true}}}
  ]
}`

func TestPlanManagedPolicies(t *testing.T) {
	var p tfjson.Plan
	if err := json.Unmarshal([]byte(planJSON), &p); err != nil {
		t.Fatal(err)
	}
	got := PlanManagedPolicies(&p)
	want := []string{
		"arn:aws:iam::aws:policy/AmazonEKSClusterPolicy",
		"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
		"arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicyV2",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	if PlanManagedPolicies(nil) != nil {
		t.Error("nil plan: want nil")
	}
}

type fakeIAM map[string]error // ARN -> GetPolicy error; missing key = exists

func (f fakeIAM) GetPolicy(_ context.Context, in *iam.GetPolicyInput, _ ...func(*iam.Options)) (*iam.GetPolicyOutput, error) {
	if err := f[aws.ToString(in.PolicyArn)]; err != nil {
		return nil, err
	}
	return &iam.GetPolicyOutput{}, nil
}

func TestCheckManagedPolicies(t *testing.T) {
	const ok, typo, other = "arn:aws:iam::aws:policy/A", "arn:aws:iam::aws:policy/BV2", "arn:aws:iam::aws:policy/C"
	nse := &iamtypes.NoSuchEntityException{Message: aws.String("not found")}
	tests := []struct {
		name    string
		iam     fakeIAM
		arns    []string
		wantErr string
	}{
		{"all exist", fakeIAM{}, []string{ok, other}, ""},
		{"none", fakeIAM{}, nil, ""},
		{"typo named", fakeIAM{typo: nse}, []string{ok, typo}, "do not exist (check the names in infra/terraform/aws): " + typo},
		{"every missing one named", fakeIAM{typo: nse, other: nse}, []string{typo, other}, typo + ", " + other},
		{"other errors stop", fakeIAM{ok: errors.New("AccessDenied")}, []string{ok}, "check AWS managed policy " + ok + ": AccessDenied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckManagedPolicies(context.Background(), tt.iam, tt.arns)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
