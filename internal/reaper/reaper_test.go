package reaper

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

const acct = "arn:aws:%s:ap-south-1:111122223333:%s"

func arn(service, resource string) string { return fmt.Sprintf(acct, service, resource) }

func res(a string, tags ...string) Resource {
	r := Resource{ARN: a, Tags: map[string]string{}}
	for i := 0; i+1 < len(tags); i += 2 {
		r.Tags[tags[i]] = tags[i+1]
	}
	return r
}

func expired(a string) Resource {
	return res(a, "lab", "true", "expires-at", now.Add(-time.Hour).Format(time.RFC3339))
}

type fakeLister struct {
	rs  []Resource
	err error
}

func (f fakeLister) List(context.Context) ([]Resource, error) { return f.rs, f.err }

type fakeDeleter struct {
	got  []string
	fail map[string]bool
	wait map[string]bool
}

func (f *fakeDeleter) Delete(_ context.Context, t Target) error {
	f.got = append(f.got, string(t.Kind)+" "+t.ID)
	if f.fail[t.ID] {
		return errors.New("DependencyViolation")
	}
	if f.wait[t.ID] {
		return fmt.Errorf("%w: 1 node groups still deleting", ErrNotYet)
	}
	return nil
}

func TestRun(t *testing.T) {
	tests := []struct {
		name    string
		rs      []Resource
		dryRun  bool
		fail    map[string]bool
		wait    map[string]bool
		deletes []string // calls to the deleter, in order
		reports []string // Result.Deleted as "kind id"
		kept    int
		skipped int
		waiting int
		errors  int
	}{
		{
			name: "deletes in dependency order, keeps the rest",
			rs: []Resource{
				expired(arn("ec2", "elastic-ip/eipalloc-1")),
				expired(arn("ec2", "volume/vol-1")),
				expired(arn("ec2", "instance/i-1")),
				expired(arn("elasticloadbalancing", "loadbalancer/app/web/abc")),
				expired(arn("eks", "nodegroup/lab/spot/uuid-1")),
				res(arn("ec2", "instance/i-2"), "lab", "true", "expires-at", now.Add(time.Hour).Format(time.RFC3339)),
				res(arn("s3", "lab-tfstate-111122223333"), "lab", "true", "owner", "me"), // baseline: no expires-at
			},
			deletes: []string{"eks-nodegroup lab/spot", "ec2-instance i-1", "load-balancer " + arn("elasticloadbalancing", "loadbalancer/app/web/abc"),
				"ebs-volume vol-1", "elastic-ip eipalloc-1"},
			kept: 2,
		},
		{
			// A cloud lab past its TTL: the system node group, its instance
			// (EKS removes it with the node group), two Karpenter spot nodes,
			// an NLB from the Load Balancer Controller, the cluster, and a
			// leftover volume, listed in the tagging API's arbitrary order.
			name: "a whole EKS lab goes node groups, instances, load balancers, cluster, volumes",
			rs: []Resource{
				expired(arn("eks", "cluster/lab")),
				expired(arn("ec2", "volume/vol-pvc")),
				expired(arn("elasticloadbalancing", "loadbalancer/net/k8s-podinfo/1")),
				res(arn("ec2", "instance/i-karpenter-1"), "lab", "true", "expires-at", now.Add(-time.Hour).Format(time.RFC3339),
					"karpenter.sh/nodepool", "spot"),
				res(arn("ec2", "instance/i-system"), "lab", "true", "expires-at", now.Add(-time.Hour).Format(time.RFC3339),
					NodeGroupTag, "system"),
				expired(arn("eks", "nodegroup/lab/system/uuid-1")),
				expired(arn("ec2", "instance/i-karpenter-2")),
			},
			deletes: []string{
				"eks-nodegroup lab/system",
				"ec2-instance i-karpenter-1", "ec2-instance i-karpenter-2",
				"load-balancer " + arn("elasticloadbalancing", "loadbalancer/net/k8s-podinfo/1"),
				"eks-cluster lab",
				"ebs-volume vol-pvc",
			},
			kept: 1,
		},
		{
			name:    "a cluster with node groups still deleting waits for the next run",
			rs:      []Resource{expired(arn("eks", "cluster/lab")), expired(arn("eks", "nodegroup/lab/system/uuid-1"))},
			wait:    map[string]bool{"lab": true},
			deletes: []string{"eks-nodegroup lab/system", "eks-cluster lab"},
			reports: []string{"eks-nodegroup lab/system"},
			waiting: 1,
		},
		{
			name:    "dry run deletes nothing and reports what it would",
			rs:      []Resource{expired(arn("ec2", "instance/i-1")), expired(arn("ec2", "volume/vol-1"))},
			dryRun:  true,
			reports: []string{"ec2-instance i-1", "ebs-volume vol-1"},
		},
		{
			name:    "expires-at exactly now is expired",
			rs:      []Resource{res(arn("ec2", "instance/i-1"), "lab", "true", "expires-at", now.Format(time.RFC3339))},
			deletes: []string{"ec2-instance i-1"},
		},
		{
			name: "bad expires-at and unknown types are skipped, never deleted",
			rs: []Resource{
				res(arn("ec2", "instance/i-1"), "lab", "true", "expires-at", "tomorrow"),
				expired(arn("elasticloadbalancing", "loadbalancer/classic-lb")),
				expired(arn("rds", "db:lab")),
			},
			skipped: 3,
		},
		{
			name: "without lab=true it is kept",
			rs:   []Resource{res(arn("ec2", "instance/i-1"), "lab", "false", "expires-at", now.Add(-time.Hour).Format(time.RFC3339))},
			kept: 1,
		},
		{
			name:    "a failed delete is reported and the run goes on",
			rs:      []Resource{expired(arn("ec2", "volume/vol-1")), expired(arn("ec2", "elastic-ip/eipalloc-1"))},
			fail:    map[string]bool{"vol-1": true},
			deletes: []string{"ebs-volume vol-1", "elastic-ip eipalloc-1"},
			reports: []string{"elastic-ip eipalloc-1"},
			errors:  1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &fakeDeleter{fail: tt.fail, wait: tt.wait}
			var logs []string
			r, err := Run(context.Background(), fakeLister{rs: tt.rs}, d, now, tt.dryRun, func(f string, a ...any) {
				logs = append(logs, fmt.Sprintf(f, a...))
			})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(d.got, tt.deletes) {
				t.Errorf("deletes = %q\nwant      %q", d.got, tt.deletes)
			}
			want := tt.reports
			if want == nil {
				want = tt.deletes
			}
			var got []string
			for _, x := range r.Deleted {
				got = append(got, string(x.Kind)+" "+x.ID)
			}
			if !slices.Equal(got, want) {
				t.Errorf("reported = %q, want %q", got, want)
			}
			if r.Kept != tt.kept || len(r.Skipped) != tt.skipped || len(r.Waiting) != tt.waiting || len(r.Errors) != tt.errors || r.DryRun != tt.dryRun {
				t.Errorf("kept %d skipped %d waiting %d errors %d dry %v; want %d %d %d %d %v", r.Kept, len(r.Skipped), len(r.Waiting),
					len(r.Errors), r.DryRun, tt.kept, tt.skipped, tt.waiting, tt.errors, tt.dryRun)
			}
			if tt.dryRun && (len(logs) == 0 || !strings.HasPrefix(logs[0], "dry run: would delete ec2-instance i-1 (expired 1h0m0s ago)")) {
				t.Errorf("dry-run log = %q", logs)
			}
		})
	}
}

func TestRunListError(t *testing.T) {
	_, err := Run(context.Background(), fakeLister{err: errors.New("AccessDenied")}, &fakeDeleter{}, now, false, func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("err = %v", err)
	}
}

func TestParseARN(t *testing.T) {
	tests := []struct {
		arn  string
		kind Kind
		id   string
		err  bool
	}{
		{arn("ec2", "instance/i-0abc"), Instance, "i-0abc", false},
		{arn("ec2", "volume/vol-0abc"), Volume, "vol-0abc", false},
		{arn("ec2", "elastic-ip/eipalloc-0abc"), Address, "eipalloc-0abc", false},
		{arn("eks", "nodegroup/lab/spot-a/1a2b"), NodeGroup, "lab/spot-a", false},
		{arn("elasticloadbalancing", "loadbalancer/net/api/123"), LoadBalancer, arn("elasticloadbalancing", "loadbalancer/net/api/123"), false},
		{arn("eks", "cluster/lab"), Cluster, "lab", false},
		{arn("eks", "nodegroup/lab"), "", "", true},
		{arn("eks", "cluster/"), "", "", true},
		{arn("elasticloadbalancing", "loadbalancer/old"), "", "", true},
		{arn("ec2", "security-group/sg-1"), "", "", true},
		{"not-an-arn", "", "", true},
	}
	for _, tt := range tests {
		kind, id, err := parseARN(tt.arn)
		if (err != nil) != tt.err || kind != tt.kind || id != tt.id {
			t.Errorf("parseARN(%q) = %q %q %v; want %q %q err %v", tt.arn, kind, id, err, tt.kind, tt.id, tt.err)
		}
	}
}
