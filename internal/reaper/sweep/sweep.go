// Package sweep is lab down's safety net: after OpenTofu has destroyed a
// cloud lab, it deletes what is still tagged with that lab's owner and
// expires-at. It reuses the reaper's lookups and deletes but lives outside
// the reaper package, so the reaper Lambda's binary does not change.
package sweep

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/chanupen/devops-lab/internal/reaper"
)

// OwnerTag names who a lab resource belongs to.
const OwnerTag = "owner"

// Kinds are what lab down's final sweep deletes once OpenTofu has
// destroyed the cluster, in the reaper's delete order (an ENI first, as it
// can hold a volume's attachment; secrets last): what controllers and lab
// create outside OpenTofu and can outlive the cluster (an ENI the VPC CNI
// left, a volume the EBS CSI driver made, a Secrets Manager secret).
var Kinds = []reaper.Kind{reaper.NetworkInterface, reaper.Volume, reaper.Secret}

// Result is what a sweep did.
type Result struct {
	Deleted []reaper.Target
	Waiting []string // still in use (reaper.ErrNotYet): "<arn>: why"
	Errors  []string
}

// Run deletes the resources of one environment that are still there
// after its OpenTofu destroy: every resource of kinds tagged lab=true and
// with each tag in env (owner and expires-at, which only this environment
// carries). Each one is looked up directly first: gone ones are skipped,
// and live tags win over the tagging API's. It deletes in the reaper's
// order and never waits: an in-use resource is reported as waiting.
//
// Unlike reaper.Run it does not need expires-at to have passed: lab down calls it
// for an environment it has just destroyed. env must name the owner and
// the expires-at, so it can never reach the baseline (no expires-at) or
// another environment.
func Run(ctx context.Context, l reaper.Lister, c reaper.Checker, d reaper.Deleter, env map[string]string, kinds []reaper.Kind, logf func(string, ...any)) (Result, error) {
	var res Result
	if env[OwnerTag] == "" || env[reaper.ExpiresTag] == "" {
		return res, fmt.Errorf("sweep needs the environment's %s and %s tags", OwnerTag, reaper.ExpiresTag)
	}
	rs, err := l.List(ctx)
	if err != nil {
		return res, err
	}
	var targets []reaper.Target
	seen := map[string]bool{}
	for _, r := range rs {
		if seen[r.ARN] {
			continue
		}
		seen[r.ARN] = true
		kind, id, err := kindOf(r.ARN)
		if err != nil || !slices.Contains(kinds, kind) || !inEnv(r.Tags, env) {
			continue
		}
		live, err := c.Check(ctx, r.ARN)
		switch {
		case err != nil:
			res.Errors = append(res.Errors, fmt.Sprintf("check %s: %v", r.ARN, err))
			continue
		case live.Gone:
			continue
		case live.Tags != nil && !inEnv(live.Tags, env):
			logf("keep %s: its live tags are not this environment's", r.ARN)
			continue
		}
		targets = append(targets, reaper.Target{Kind: kind, ARN: r.ARN, ID: id})
	}
	sort.SliceStable(targets, func(i, j int) bool {
		return slices.Index(Kinds, targets[i].Kind) < slices.Index(Kinds, targets[j].Kind)
	})
	for _, t := range targets {
		if err := d.Delete(ctx, t); err != nil {
			if errors.Is(err, reaper.ErrNotYet) {
				res.Waiting = append(res.Waiting, fmt.Sprintf("%s: %v", t.ARN, err))
			} else {
				res.Errors = append(res.Errors, fmt.Sprintf("delete %s: %v", t.ARN, err))
			}
			continue
		}
		logf("deleted %s", t.ARN)
		res.Deleted = append(res.Deleted, t)
	}
	return res, nil
}

func inEnv(tags, env map[string]string) bool {
	if tags[reaper.LabTag] != "true" {
		return false
	}
	for k, v := range env {
		if tags[k] != v {
			return false
		}
	}
	return true
}

// Listers lists with each Lister in turn; a resource listed twice is
// returned twice (Sweep skips the repeat).
type Listers []reaper.Lister

// List implements Lister.
func (ls Listers) List(ctx context.Context) ([]reaper.Resource, error) {
	var out []reaper.Resource
	for _, l := range ls {
		rs, err := l.List(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, rs...)
	}
	return out, nil
}

// Lister lists the Kinds tagged lab=true with their own Describe
// and List calls. The tagging API can lag behind by up to an hour, so a
// volume made minutes before lab down may not be in it yet.
type Lister struct {
	AWS     *reaper.AWS
	Account string
	Region  string
}

// List implements Lister.
func (s Lister) List(ctx context.Context) ([]reaper.Resource, error) {
	var out []reaper.Resource
	labFilter := []ec2types.Filter{{Name: aws.String("tag:" + reaper.LabTag), Values: []string{"true"}}}
	vols := ec2.NewDescribeVolumesPaginator(s.AWS.EC2, &ec2.DescribeVolumesInput{Filters: labFilter})
	for vols.HasMorePages() {
		page, err := vols.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list volumes: %w", err)
		}
		for _, v := range page.Volumes {
			out = append(out, reaper.Resource{ARN: s.ec2ARN("volume", aws.ToString(v.VolumeId)), Tags: ec2Tags(v.Tags)})
		}
	}
	enis := ec2.NewDescribeNetworkInterfacesPaginator(s.AWS.EC2, &ec2.DescribeNetworkInterfacesInput{Filters: labFilter})
	for enis.HasMorePages() {
		page, err := enis.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list network interfaces: %w", err)
		}
		for _, n := range page.NetworkInterfaces {
			out = append(out, reaper.Resource{ARN: s.ec2ARN("network-interface", aws.ToString(n.NetworkInterfaceId)), Tags: ec2Tags(n.TagSet)})
		}
	}
	secs := secretsmanager.NewListSecretsPaginator(s.AWS.Secrets, &secretsmanager.ListSecretsInput{
		Filters: []smtypes.Filter{{Key: smtypes.FilterNameStringTypeTagKey, Values: []string{reaper.LabTag}}},
	})
	for secs.HasMorePages() {
		page, err := secs.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list secrets: %w", err)
		}
		for _, e := range page.SecretList {
			tags := map[string]string{}
			for _, t := range e.Tags {
				tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
			}
			out = append(out, reaper.Resource{ARN: aws.ToString(e.ARN), Tags: tags})
		}
	}
	return out, nil
}

func (s Lister) ec2ARN(typ, id string) string {
	return fmt.Sprintf("arn:aws:ec2:%s:%s:%s/%s", s.Region, s.Account, typ, id)
}

// kindOf maps an ARN of one of Kinds to the kind and the ID reaper.AWS's
// Delete takes for it (as the reaper's own ARN parser does). It is here,
// not exported from the reaper, because any change to the reaper package
// changes the Lambda's binary and so the account baseline.
func kindOf(arn string) (reaper.Kind, string, error) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" {
		return "", "", fmt.Errorf("not an ARN: %s", arn)
	}
	service, resource := parts[2], parts[5]
	switch {
	case service == "ec2" && strings.HasPrefix(resource, "volume/") && len(resource) > len("volume/"):
		return reaper.Volume, strings.TrimPrefix(resource, "volume/"), nil
	case service == "ec2" && strings.HasPrefix(resource, "network-interface/") && len(resource) > len("network-interface/"):
		return reaper.NetworkInterface, strings.TrimPrefix(resource, "network-interface/"), nil
	case service == "secretsmanager" && strings.HasPrefix(resource, "secret:") && len(resource) > len("secret:"):
		return reaper.Secret, arn, nil // the delete takes the ARN
	}
	return "", "", fmt.Errorf("not swept: %s", arn)
}

func ec2Tags(in []ec2types.Tag) map[string]string {
	out := make(map[string]string, len(in))
	for _, t := range in {
		out[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return out
}
