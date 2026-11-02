package reaper

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	tagging "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	taggingtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/smithy-go"
)

// AWS lists and deletes through the AWS SDK.
type AWS struct {
	Tagging   *tagging.Client
	EC2       *ec2.Client
	EKS       *eks.Client
	ELB       *elbv2.Client
	ClassicLB *elb.Client
	ECR       *ecr.Client
}

// NewAWS builds the clients from one config.
func NewAWS(cfg aws.Config) *AWS {
	return &AWS{Tagging: tagging.NewFromConfig(cfg), EC2: ec2.NewFromConfig(cfg), EKS: eks.NewFromConfig(cfg),
		ELB: elbv2.NewFromConfig(cfg), ClassicLB: elb.NewFromConfig(cfg), ECR: ecr.NewFromConfig(cfg)}
}

// Check implements Checker with one Describe call per resource. EC2 and
// EKS lookups return the live tags too; for the other types only
// existence is checked. Types it does not know count as existing.
func (a *AWS) Check(ctx context.Context, arn string) (Live, error) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 {
		return Live{}, nil
	}
	service, resource := parts[2], parts[5]
	typ, id, _ := strings.Cut(resource, "/")
	ids := []string{id}
	var err error
	live := Live{Known: true}
	switch {
	case service == "ec2" && typ == "instance":
		var out *ec2.DescribeInstancesOutput
		if out, err = a.EC2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: ids}); err == nil {
			live = instanceLive(out)
		}
	case service == "ec2" && typ == "volume":
		var out *ec2.DescribeVolumesOutput
		if out, err = a.EC2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: ids}); err == nil {
			live = ec2Live(len(out.Volumes), func() []ec2types.Tag { return out.Volumes[0].Tags })
		}
	case service == "ec2" && typ == "security-group":
		var out *ec2.DescribeSecurityGroupsOutput
		if out, err = a.EC2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: ids}); err == nil {
			live = ec2Live(len(out.SecurityGroups), func() []ec2types.Tag { return out.SecurityGroups[0].Tags })
		}
	case service == "ec2" && typ == "network-interface":
		var out *ec2.DescribeNetworkInterfacesOutput
		if out, err = a.EC2.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{NetworkInterfaceIds: ids}); err == nil {
			live = ec2Live(len(out.NetworkInterfaces), func() []ec2types.Tag { return out.NetworkInterfaces[0].TagSet })
		}
	case service == "ec2" && typ == "elastic-ip":
		var out *ec2.DescribeAddressesOutput
		if out, err = a.EC2.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{AllocationIds: ids}); err == nil {
			live = ec2Live(len(out.Addresses), func() []ec2types.Tag { return out.Addresses[0].Tags })
		}
	case service == "ec2" && typ == "vpc":
		var out *ec2.DescribeVpcsOutput
		if out, err = a.EC2.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: ids}); err == nil {
			live = ec2Live(len(out.Vpcs), func() []ec2types.Tag { return out.Vpcs[0].Tags })
		}
	case service == "ec2" && typ == "subnet":
		var out *ec2.DescribeSubnetsOutput
		if out, err = a.EC2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: ids}); err == nil {
			live = ec2Live(len(out.Subnets), func() []ec2types.Tag { return out.Subnets[0].Tags })
		}
	case service == "ec2" && typ == "route-table":
		var out *ec2.DescribeRouteTablesOutput
		if out, err = a.EC2.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{RouteTableIds: ids}); err == nil {
			live = ec2Live(len(out.RouteTables), func() []ec2types.Tag { return out.RouteTables[0].Tags })
		}
	case service == "ec2" && typ == "internet-gateway":
		var out *ec2.DescribeInternetGatewaysOutput
		if out, err = a.EC2.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{InternetGatewayIds: ids}); err == nil {
			live = ec2Live(len(out.InternetGateways), func() []ec2types.Tag { return out.InternetGateways[0].Tags })
		}
	case service == "ec2" && typ == "launch-template":
		var out *ec2.DescribeLaunchTemplatesOutput
		if out, err = a.EC2.DescribeLaunchTemplates(ctx, &ec2.DescribeLaunchTemplatesInput{LaunchTemplateIds: ids}); err == nil {
			live = ec2Live(len(out.LaunchTemplates), func() []ec2types.Tag { return out.LaunchTemplates[0].Tags })
		}
	case service == "eks" && typ == "cluster":
		var out *eks.DescribeClusterOutput
		if out, err = a.EKS.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(id)}); err == nil {
			live.Tags = out.Cluster.Tags
		}
	case service == "eks" && typ == "nodegroup":
		p := strings.Split(id, "/")
		if len(p) < 2 {
			return Live{}, nil
		}
		var out *eks.DescribeNodegroupOutput
		if out, err = a.EKS.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{ClusterName: aws.String(p[0]), NodegroupName: aws.String(p[1])}); err == nil {
			live.Tags = out.Nodegroup.Tags
		}
	case service == "elasticloadbalancing" && typ == "loadbalancer" && strings.Contains(id, "/"):
		_, err = a.ELB.DescribeLoadBalancers(ctx, &elbv2.DescribeLoadBalancersInput{LoadBalancerArns: []string{arn}})
	case service == "elasticloadbalancing" && typ == "loadbalancer":
		_, err = a.ClassicLB.DescribeLoadBalancers(ctx, &elb.DescribeLoadBalancersInput{LoadBalancerNames: ids})
	case service == "elasticloadbalancing" && typ == "targetgroup":
		_, err = a.ELB.DescribeTargetGroups(ctx, &elbv2.DescribeTargetGroupsInput{TargetGroupArns: []string{arn}})
	case service == "ecr" && typ == "repository":
		_, err = a.ECR.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{RepositoryNames: ids})
	default:
		return Live{}, nil
	}
	if notFound(err) {
		return Live{Known: true, Gone: true}, nil
	}
	return live, err
}

// notFound is true for the "no such resource" errors of the APIs Check
// calls: InvalidInstanceID.NotFound, InvalidGroup.NotFound,
// ResourceNotFoundException, LoadBalancerNotFound, TargetGroupNotFound,
// RepositoryNotFoundException and the like.
func notFound(err error) bool {
	var api smithy.APIError
	if !errors.As(err, &api) {
		return false
	}
	c := api.ErrorCode()
	return strings.HasSuffix(c, "NotFound") || strings.HasSuffix(c, "NotFoundException")
}

// instanceLive: a terminated instance is gone; the API lists it for about
// an hour after.
func instanceLive(out *ec2.DescribeInstancesOutput) Live {
	if len(out.Reservations) == 0 || len(out.Reservations[0].Instances) == 0 {
		return Live{Known: true, Gone: true}
	}
	i := out.Reservations[0].Instances[0]
	if i.State != nil && i.State.Name == ec2types.InstanceStateNameTerminated {
		return Live{Known: true, Gone: true}
	}
	return Live{Known: true, Tags: ec2Tags(i.Tags)}
}

func ec2Live(n int, tags func() []ec2types.Tag) Live {
	if n == 0 {
		return Live{Known: true, Gone: true}
	}
	return Live{Known: true, Tags: ec2Tags(tags())}
}

func ec2Tags(in []ec2types.Tag) map[string]string {
	out := make(map[string]string, len(in))
	for _, t := range in {
		out[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return out
}

// InVPC implements VPCScanner. Tags do not matter here: a plain
// type: LoadBalancer Service makes an untagged Classic Load Balancer, and
// its network interfaces and security group would block the VPC destroy.
// Network interfaces still attached are left to whatever holds them (an
// instance or a load balancer deleted in the same run).
func (a *AWS) InVPC(ctx context.Context, vpcID string) ([]Target, error) {
	var out []Target
	byVPC := []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}}

	lbs := elbv2.NewDescribeLoadBalancersPaginator(a.ELB, &elbv2.DescribeLoadBalancersInput{})
	for lbs.HasMorePages() {
		page, err := lbs.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe load balancers: %w", err)
		}
		for _, lb := range page.LoadBalancers {
			if aws.ToString(lb.VpcId) == vpcID {
				out = append(out, Target{Kind: LoadBalancer, ARN: aws.ToString(lb.LoadBalancerArn), ID: aws.ToString(lb.LoadBalancerArn)})
			}
		}
	}
	classic := elb.NewDescribeLoadBalancersPaginator(a.ClassicLB, &elb.DescribeLoadBalancersInput{})
	for classic.HasMorePages() {
		page, err := classic.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe classic load balancers: %w", err)
		}
		for _, lb := range page.LoadBalancerDescriptions {
			if aws.ToString(lb.VPCId) == vpcID {
				out = append(out, Target{Kind: ClassicLB, ID: aws.ToString(lb.LoadBalancerName)})
			}
		}
	}
	tgs := elbv2.NewDescribeTargetGroupsPaginator(a.ELB, &elbv2.DescribeTargetGroupsInput{})
	for tgs.HasMorePages() {
		page, err := tgs.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe target groups: %w", err)
		}
		for _, tg := range page.TargetGroups {
			if aws.ToString(tg.VpcId) == vpcID {
				out = append(out, Target{Kind: TargetGroup, ARN: aws.ToString(tg.TargetGroupArn), ID: aws.ToString(tg.TargetGroupArn)})
			}
		}
	}
	enis := ec2.NewDescribeNetworkInterfacesPaginator(a.EC2, &ec2.DescribeNetworkInterfacesInput{
		Filters: append(byVPC, ec2types.Filter{Name: aws.String("status"), Values: []string{"available"}})})
	for enis.HasMorePages() {
		page, err := enis.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe network interfaces: %w", err)
		}
		for _, n := range page.NetworkInterfaces {
			out = append(out, Target{Kind: NetworkInterface, ID: aws.ToString(n.NetworkInterfaceId)})
		}
	}
	sgs := ec2.NewDescribeSecurityGroupsPaginator(a.EC2, &ec2.DescribeSecurityGroupsInput{Filters: byVPC})
	for sgs.HasMorePages() {
		page, err := sgs.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe security groups: %w", err)
		}
		for _, g := range page.SecurityGroups {
			if aws.ToString(g.GroupName) != "default" { // every VPC has one; it goes with the VPC
				out = append(out, Target{Kind: SecurityGroup, ID: aws.ToString(g.GroupId)})
			}
		}
	}
	return out, nil
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
		_, err = a.ELB.DeleteLoadBalancer(ctx, &elbv2.DeleteLoadBalancerInput{LoadBalancerArn: aws.String(t.ID)})
	case ClassicLB:
		_, err = a.ClassicLB.DeleteLoadBalancer(ctx, &elb.DeleteLoadBalancerInput{LoadBalancerName: aws.String(t.ID)})
	case NetworkInterface:
		_, err = a.EC2.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{NetworkInterfaceId: aws.String(t.ID)})
	case TargetGroup:
		_, err = a.ELB.DeleteTargetGroup(ctx, &elbv2.DeleteTargetGroupInput{TargetGroupArn: aws.String(t.ID)})
	default:
		err = fmt.Errorf("no delete for kind %q", t.Kind)
	}
	return inUse(err)
}

// inUseCodes are API errors that clear up once a dependency is gone.
var inUseCodes = []string{"DependencyViolation", "ResourceInUse", "ResourceInUseException", "InvalidVolume.InUse", "IncorrectState",
	"InvalidNetworkInterface.InUse"}

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
// group), then deletes it. A group that a network interface still uses
// waits, and its references stay: revoking them would cut traffic to
// something still running.
func (a *AWS) deleteSecurityGroup(ctx context.Context, id string) error {
	used, err := a.EC2.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{{Name: aws.String("group-id"), Values: []string{id}}}, MaxResults: aws.Int32(5)})
	if err != nil {
		return err
	}
	if n := len(used.NetworkInterfaces); n > 0 {
		return fmt.Errorf("%w: used by network interface %s", ErrNotYet, aws.ToString(used.NetworkInterfaces[0].NetworkInterfaceId))
	}
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

// CodeBuild runs the teardown project.
type CodeBuild struct {
	Client  *codebuild.Client
	Project string
}

// Running implements Teardown: the newest builds are enough, there is
// only ever one at a time.
func (c CodeBuild) Running(ctx context.Context) (string, error) {
	list, err := c.Client.ListBuildsForProject(ctx, &codebuild.ListBuildsForProjectInput{
		ProjectName: aws.String(c.Project), SortOrder: cbtypes.SortOrderTypeDescending})
	if err != nil || len(list.Ids) == 0 {
		return "", err
	}
	ids := list.Ids
	if len(ids) > 5 {
		ids = ids[:5]
	}
	got, err := c.Client.BatchGetBuilds(ctx, &codebuild.BatchGetBuildsInput{Ids: ids})
	if err != nil {
		return "", err
	}
	for _, b := range got.Builds {
		if b.BuildStatus == cbtypes.StatusTypeInProgress {
			return aws.ToString(b.Id), nil
		}
	}
	return "", nil
}

// Get implements Teardown.
func (c CodeBuild) Get(ctx context.Context, id string) (Build, error) {
	got, err := c.Client.BatchGetBuilds(ctx, &codebuild.BatchGetBuildsInput{Ids: []string{id}})
	if err != nil {
		return Build{}, err
	}
	if len(got.Builds) == 0 {
		return Build{ID: id, State: "UNKNOWN"}, nil // expired from CodeBuild's history
	}
	b := got.Builds[0]
	return Build{ID: id, State: string(b.BuildStatus), End: aws.ToTime(b.EndTime)}, nil
}

func attemptVar(attempt int) string {
	if attempt <= 0 {
		return "unknown"
	}
	return strconv.Itoa(attempt)
}

// Start implements Teardown. The build reads ATTEMPT ("unknown" when the
// reaper could not read its count) and MAX_ATTEMPTS for its summary email.
func (c CodeBuild) Start(ctx context.Context, attempt, maxAttempts int) (string, error) {
	out, err := c.Client.StartBuild(ctx, &codebuild.StartBuildInput{
		ProjectName: aws.String(c.Project),
		EnvironmentVariablesOverride: []cbtypes.EnvironmentVariable{
			{Name: aws.String("ATTEMPT"), Value: aws.String(attemptVar(attempt)), Type: cbtypes.EnvironmentVariableTypePlaintext},
			{Name: aws.String("MAX_ATTEMPTS"), Value: aws.String(strconv.Itoa(maxAttempts)), Type: cbtypes.EnvironmentVariableTypePlaintext},
		},
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.Build.Id), nil
}

// Check implements Teardown.
func (c CodeBuild) Check(ctx context.Context) error {
	out, err := c.Client.BatchGetProjects(ctx, &codebuild.BatchGetProjectsInput{Names: []string{c.Project}})
	if err != nil {
		return err
	}
	if len(out.Projects) == 0 {
		return fmt.Errorf("CodeBuild project %s not found", c.Project)
	}
	return nil
}

// S3API is the part of the S3 client the store uses.
type S3API interface {
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// S3Store keeps TeardownState at TeardownStateKey in the state bucket.
type S3Store struct {
	Client S3API
	Bucket string
}

// Load implements StateStore. A missing object is the zero state.
func (s S3Store) Load(ctx context.Context) (TeardownState, error) {
	var st TeardownState
	b, found, err := getObject(ctx, s.Client, s.Bucket, TeardownStateKey, 64<<10)
	if err != nil || !found {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

// getObject reads one object; found is false when there is none. S3
// reports a missing key as 403 AccessDenied, not 404, to callers that may
// not list the bucket; then a listing of the key itself decides.
func getObject(ctx context.Context, c S3API, bucket, key string, limit int64) (body []byte, found bool, err error) {
	out, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	var nsk *s3types.NoSuchKey
	switch {
	case errors.As(err, &nsk):
		return nil, false, nil
	case accessDenied(err):
		if missing, lerr := missingKey(ctx, c, bucket, key); lerr == nil && missing {
			return nil, false, nil
		}
		return nil, false, err
	case err != nil:
		return nil, false, err
	}
	defer func() { _ = out.Body.Close() }()
	body, err = io.ReadAll(io.LimitReader(out.Body, limit))
	return body, err == nil, err
}

// missingKey lists the key as a prefix: true when the key is not there.
func missingKey(ctx context.Context, c S3API, bucket, key string) (bool, error) {
	out, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(key), MaxKeys: aws.Int32(1)})
	if err != nil {
		return false, err
	}
	for _, o := range out.Contents {
		if aws.ToString(o.Key) == key {
			return false, nil
		}
	}
	return true, nil
}

// S3Stacks implements StackReader from the state bucket.
type S3Stacks struct {
	Client S3API
	Bucket string
}

// Stack implements StackReader.
func (s S3Stacks) Stack(ctx context.Context, name string) (StackInfo, error) {
	info := StackInfo{Name: name}
	key, ok := TofuStacks[name]
	if !ok {
		return info, fmt.Errorf("unknown stack %q", name)
	}
	state, found, err := getObject(ctx, s.Client, s.Bucket, key, 64<<20)
	if err != nil {
		return info, fmt.Errorf("state %s: %w", key, err)
	}
	if found {
		if info.Resources, err = ManagedResources(state); err != nil {
			return info, fmt.Errorf("state %s: %w", key, err)
		}
	}
	bundle, found, err := getObject(ctx, s.Client, s.Bucket, BundleKey(name), 16<<20)
	if err != nil {
		return info, fmt.Errorf("bundle %s: %w", BundleKey(name), err)
	}
	if found {
		info.Bundle = true
		if info.Expires, err = BundleExpiry(bundle); err != nil {
			return info, fmt.Errorf("bundle %s: %w", BundleKey(name), err)
		}
	}
	return info, nil
}

// ManagedResources counts the managed resources (not data sources) in an
// OpenTofu state file.
func ManagedResources(state []byte) (int, error) {
	var st struct {
		Resources []struct {
			Mode string `json:"mode"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(state, &st); err != nil {
		return 0, err
	}
	n := 0
	for _, r := range st.Resources {
		if r.Mode == "managed" {
			n++
		}
	}
	return n, nil
}

// BundleVarsFile is the variables file lab writes into each module, and
// so into its teardown bundle.
const BundleVarsFile = "lab.auto.tfvars.json"

// BundleExpiry is the expires_at a teardown bundle was applied (or last
// extended) with.
func BundleExpiry(bundle []byte) (time.Time, error) {
	zr, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		return time.Time{}, err
	}
	f, err := zr.Open(BundleVarsFile)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = f.Close() }()
	var vars struct {
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(f).Decode(&vars); err != nil {
		return time.Time{}, fmt.Errorf("%s: %w", BundleVarsFile, err)
	}
	t, err := time.Parse(time.RFC3339, vars.ExpiresAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: expires_at %q: %w", BundleVarsFile, vars.ExpiresAt, err)
	}
	return t, nil
}

func accessDenied(err error) bool {
	var api smithy.APIError
	return errors.As(err, &api) && api.ErrorCode() == "AccessDenied"
}

// Save implements StateStore.
func (s S3Store) Save(ctx context.Context, st TeardownState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.Client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(TeardownStateKey),
		Body: bytes.NewReader(b), ContentType: aws.String("application/json")})
	return err
}

// Clear implements StateStore; clearing nothing is not an error.
func (s S3Store) Clear(ctx context.Context) error {
	_, err := s.Client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(TeardownStateKey)})
	return err
}

// SNS emails through the lab-teardown topic.
type SNS struct {
	Client *sns.Client
	Topic  string
}

// Notify implements Notifier.
func (n SNS) Notify(ctx context.Context, subject, body string) error {
	_, err := n.Client.Publish(ctx, &sns.PublishInput{TopicArn: aws.String(n.Topic), Subject: aws.String(subject), Message: aws.String(body)})
	return err
}
