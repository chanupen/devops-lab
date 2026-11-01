package reaper

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	tagging "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	taggingtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
	"github.com/aws/smithy-go"
)

// AWS lists and deletes through the AWS SDK.
type AWS struct {
	Tagging *tagging.Client
	EC2     *ec2.Client
	EKS     *eks.Client
	ELB     *elbv2.Client
}

// NewAWS builds the clients from one config.
func NewAWS(cfg aws.Config) *AWS {
	return &AWS{Tagging: tagging.NewFromConfig(cfg), EC2: ec2.NewFromConfig(cfg), EKS: eks.NewFromConfig(cfg), ELB: elbv2.NewFromConfig(cfg)}
}

// List implements Lister with the Resource Groups Tagging API: every
// resource tagged lab=true, of any type, so tofu-managed ones of types the
// reaper cannot delete still trigger the teardown.
func (a *AWS) List(ctx context.Context) ([]Resource, error) {
	var out []Resource
	p := tagging.NewGetResourcesPaginator(a.Tagging, &tagging.GetResourcesInput{
		TagFilters: []taggingtypes.TagFilter{{Key: aws.String(LabTag), Values: []string{"true"}}},
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, m := range page.ResourceTagMappingList {
			r := Resource{ARN: aws.ToString(m.ResourceARN), Tags: map[string]string{}}
			for _, t := range m.Tags {
				r.Tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
			}
			out = append(out, r)
		}
	}
	return out, nil
}

// Delete implements Deleter. Errors that mean "something still uses it"
// become ErrNotYet, so the next run retries instead of failing this one.
func (a *AWS) Delete(ctx context.Context, t Target) error {
	var err error
	switch t.Kind {
	case Instance:
		_, err = a.EC2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{t.ID}})
	case Volume:
		_, err = a.EC2.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: aws.String(t.ID)})
	case Address:
		_, err = a.EC2.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{AllocationId: aws.String(t.ID)})
	case SecurityGroup:
		err = a.deleteSecurityGroup(ctx, t.ID)
	case NodeGroup:
		cluster, ng, _ := strings.Cut(t.ID, "/")
		_, err = a.EKS.DeleteNodegroup(ctx, &eks.DeleteNodegroupInput{ClusterName: aws.String(cluster), NodegroupName: aws.String(ng)})
	case Cluster:
		err = a.deleteCluster(ctx, t.ID)
	case LoadBalancer:
		_, err = a.ELB.DeleteLoadBalancer(ctx, &elbv2.DeleteLoadBalancerInput{LoadBalancerArn: aws.String(t.ARN)})
	case TargetGroup:
		_, err = a.ELB.DeleteTargetGroup(ctx, &elbv2.DeleteTargetGroupInput{TargetGroupArn: aws.String(t.ARN)})
	default:
		err = fmt.Errorf("no delete for kind %q", t.Kind)
	}
	return inUse(err)
}

// inUseCodes are API errors that clear up once a dependency is gone.
var inUseCodes = []string{"DependencyViolation", "ResourceInUse", "ResourceInUseException", "InvalidVolume.InUse", "IncorrectState"}

func inUse(err error) error {
	var api smithy.APIError
	if errors.As(err, &api) {
		for _, c := range inUseCodes {
			if api.ErrorCode() == c {
				return fmt.Errorf("%w: %s", ErrNotYet, api.ErrorMessage())
			}
		}
	}
	return err
}

// deleteSecurityGroup removes the rules other groups hold that point at
// this one (the Load Balancer Controller adds one to the node security
// group), then deletes it.
func (a *AWS) deleteSecurityGroup(ctx context.Context, id string) error {
	refs, err := a.EC2.DescribeSecurityGroupRules(ctx, &ec2.DescribeSecurityGroupRulesInput{
		Filters: []ec2types.Filter{{Name: aws.String("referenced-security-group-id"), Values: []string{id}}},
	})
	if err != nil {
		return err
	}
	byGroup := map[string][]string{}
	for _, r := range refs.SecurityGroupRules {
		if aws.ToString(r.GroupId) != id && !aws.ToBool(r.IsEgress) {
			byGroup[aws.ToString(r.GroupId)] = append(byGroup[aws.ToString(r.GroupId)], aws.ToString(r.SecurityGroupRuleId))
		}
	}
	groups := make([]string, 0, len(byGroup))
	for g := range byGroup {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		if _, err := a.EC2.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
			GroupId: aws.String(g), SecurityGroupRuleIds: byGroup[g]}); err != nil {
			return err
		}
	}
	_, err = a.EC2.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: aws.String(id)})
	return err
}

// deleteCluster deletes an EKS cluster once it has no node groups. EKS
// refuses earlier, and node group deletion takes several minutes, so a
// cluster whose node groups are still there waits for the next run.
func (a *AWS) deleteCluster(ctx context.Context, name string) error {
	ngs, err := a.EKS.ListNodegroups(ctx, &eks.ListNodegroupsInput{ClusterName: aws.String(name)})
	if err != nil {
		return err
	}
	if n := len(ngs.Nodegroups); n > 0 {
		return fmt.Errorf("%w: %d node groups still deleting", ErrNotYet, n)
	}
	_, err = a.EKS.DeleteCluster(ctx, &eks.DeleteClusterInput{Name: aws.String(name)})
	return err
}

// CodeBuild starts the teardown project.
type CodeBuild struct {
	Client  *codebuild.Client
	Project string
}

// Start implements Teardown: one build at a time.
func (c CodeBuild) Start(ctx context.Context) (string, error) {
	list, err := c.Client.ListBuildsForProject(ctx, &codebuild.ListBuildsForProjectInput{
		ProjectName: aws.String(c.Project), SortOrder: cbtypes.SortOrderTypeDescending})
	if err != nil {
		return "", err
	}
	if ids := list.Ids; len(ids) > 0 {
		if len(ids) > 5 {
			ids = ids[:5]
		}
		got, err := c.Client.BatchGetBuilds(ctx, &codebuild.BatchGetBuildsInput{Ids: ids})
		if err != nil {
			return "", err
		}
		for _, b := range got.Builds {
			if b.BuildStatus == cbtypes.StatusTypeInProgress {
				return "build " + aws.ToString(b.Id) + " already running", nil
			}
		}
	}
	out, err := c.Client.StartBuild(ctx, &codebuild.StartBuildInput{ProjectName: aws.String(c.Project)})
	if err != nil {
		return "", err
	}
	return "started build " + aws.ToString(out.Build.Id), nil
}
