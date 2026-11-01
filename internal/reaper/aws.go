package reaper

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	tagging "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	taggingtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

// ResourceTypes are the tagging API types the reaper asks for.
var ResourceTypes = []string{"ec2:instance", "ec2:volume", "ec2:elastic-ip", "eks:nodegroup", "elasticloadbalancing:loadbalancer"}

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

// List implements Lister with the Resource Groups Tagging API.
func (a *AWS) List(ctx context.Context) ([]Resource, error) {
	var out []Resource
	p := tagging.NewGetResourcesPaginator(a.Tagging, &tagging.GetResourcesInput{
		TagFilters:          []taggingtypes.TagFilter{{Key: aws.String(LabTag), Values: []string{"true"}}},
		ResourceTypeFilters: ResourceTypes,
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

// Delete implements Deleter.
func (a *AWS) Delete(ctx context.Context, t Target) error {
	var err error
	switch t.Kind {
	case Instance:
		_, err = a.EC2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{t.ID}})
	case Volume:
		_, err = a.EC2.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: aws.String(t.ID)})
	case Address:
		_, err = a.EC2.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{AllocationId: aws.String(t.ID)})
	case NodeGroup:
		cluster, ng, _ := strings.Cut(t.ID, "/")
		_, err = a.EKS.DeleteNodegroup(ctx, &eks.DeleteNodegroupInput{ClusterName: aws.String(cluster), NodegroupName: aws.String(ng)})
	case LoadBalancer:
		_, err = a.ELB.DeleteLoadBalancer(ctx, &elbv2.DeleteLoadBalancerInput{LoadBalancerArn: aws.String(t.ARN)})
	default:
		err = fmt.Errorf("no delete for kind %q", t.Kind)
	}
	return err
}
