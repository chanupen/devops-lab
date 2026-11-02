package aws

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	tagging "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	taggingtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"

	"github.com/chanupen/devops-lab/internal/reaper"
)

// Tagged is one resource the tagging API reports.
type Tagged struct {
	ARN  string
	Tags map[string]string
}

// Component is the resource's component tag (baseline, registry, cluster),
// or "" when it has none (for example a load balancer from the controller).
func (t Tagged) Component() string { return t.Tags["component"] }

// listTagged returns resources matching every filter.
func listTagged(ctx context.Context, c *tagging.Client, filters []taggingtypes.TagFilter, types []string) ([]Tagged, error) {
	var out []Tagged
	p := tagging.NewGetResourcesPaginator(c, &tagging.GetResourcesInput{TagFilters: filters, ResourceTypeFilters: types})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, m := range page.ResourceTagMappingList {
			t := Tagged{ARN: aws.ToString(m.ResourceARN), Tags: map[string]string{}}
			for _, kv := range m.Tags {
				t.Tags[aws.ToString(kv.Key)] = aws.ToString(kv.Value)
			}
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ARN < out[j].ARN })
	return out, nil
}

var labFilter = taggingtypes.TagFilter{Key: aws.String("lab"), Values: []string{"true"}}

// Expiring lists lab resources that carry expires-at: everything a cloud
// lab created, and nothing from the permanent baseline.
func Expiring(ctx context.Context, cfg aws.Config) ([]Tagged, error) {
	return listTagged(ctx, tagging.NewFromConfig(cfg), []taggingtypes.TagFilter{labFilter, {Key: aws.String(ExpiresTag)}}, nil)
}

// LeftoverReport is what a cloud lab left behind.
type LeftoverReport struct {
	Left   []Tagged // still there (or could not be checked): these may bill
	Stale  []Tagged // the tagging API lists them, but they are terminated or not found
	Errors []string // failed lookups; those resources are in Left
}

// Leftovers lists every lab=true resource that is not part of the baseline,
// with a direct lookup of each: the tagging API lists deleted resources
// for up to an hour, and those go to Stale (the reaper's Gone logic).
// lab down prints them after a cloud destroy.
func Leftovers(ctx context.Context, cfg aws.Config) (LeftoverReport, error) {
	all, err := listTagged(ctx, tagging.NewFromConfig(cfg), []taggingtypes.TagFilter{labFilter}, nil)
	if err != nil {
		return LeftoverReport{}, err
	}
	return SplitLeftovers(ctx, reaper.NewAWS(cfg), withoutBaseline(all)), nil
}

// SplitLeftovers checks each resource with c and splits off the stale ones.
func SplitLeftovers(ctx context.Context, c reaper.Checker, ts []Tagged) LeftoverReport {
	rs := make([]reaper.Resource, len(ts))
	for i, t := range ts {
		rs[i] = reaper.Resource(t)
	}
	left, stale, errs := reaper.SplitStale(ctx, c, rs)
	back := func(rs []reaper.Resource) []Tagged {
		out := make([]Tagged, len(rs))
		for i, r := range rs {
			out[i] = Tagged(r)
		}
		return out
	}
	return LeftoverReport{Left: back(left), Stale: back(stale), Errors: errs}
}

func withoutBaseline(ts []Tagged) []Tagged {
	var out []Tagged
	for _, t := range ts {
		if t.Component() != "baseline" {
			out = append(out, t)
		}
	}
	return out
}

// ClusterLoadBalancers lists the load balancers the Load Balancer
// Controller created for a cluster.
func ClusterLoadBalancers(ctx context.Context, cfg aws.Config, cluster string) ([]string, error) {
	ts, err := listTagged(ctx, tagging.NewFromConfig(cfg),
		[]taggingtypes.TagFilter{{Key: aws.String("elbv2.k8s.aws/cluster"), Values: []string{cluster}}},
		[]string{"elasticloadbalancing:loadbalancer"})
	if err != nil {
		return nil, err
	}
	arns := make([]string, len(ts))
	for i, t := range ts {
		arns[i] = t.ARN
	}
	return arns, nil
}

// Retag sets expires-at on every resource that has one. The tagging API
// takes at most 20 ARNs per call; resources it cannot tag are reported,
// not fatal.
func Retag(ctx context.Context, cfg aws.Config, exp time.Time, log io.Writer) (tagged int, failed []string, err error) {
	ts, err := Expiring(ctx, cfg)
	if err != nil {
		return 0, nil, err
	}
	c := tagging.NewFromConfig(cfg)
	value := exp.UTC().Format(time.RFC3339)
	for _, batch := range batches(ts, 20) {
		arns := make([]string, len(batch))
		for i, t := range batch {
			arns[i] = t.ARN
		}
		out, err := c.TagResources(ctx, &tagging.TagResourcesInput{ResourceARNList: arns, Tags: map[string]string{ExpiresTag: value}})
		if err != nil {
			return tagged, failed, err
		}
		for arn, f := range out.FailedResourcesMap {
			failed = append(failed, fmt.Sprintf("%s: %s", arn, aws.ToString(f.ErrorMessage)))
		}
		tagged += len(arns) - len(out.FailedResourcesMap)
	}
	_, _ = fmt.Fprintf(log, "expires-at=%s on %d resources\n", value, tagged)
	sort.Strings(failed)
	return tagged, failed, nil
}

func batches[T any](s []T, n int) [][]T {
	var out [][]T
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	if len(s) > 0 {
		out = append(out, s)
	}
	return out
}

// ShortARN is service:resource without the partition, region and account,
// for one-line listings.
func ShortARN(arn string) string {
	p := strings.SplitN(arn, ":", 6)
	if len(p) != 6 {
		return arn
	}
	return p[2] + ":" + p[5]
}
