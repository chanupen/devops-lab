package reaper

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
)

// fakeChecker answers direct lookups; ARNs it does not know exist and
// carry the tags the tagging API listed.
type fakeChecker struct {
	live map[string]Live
	err  map[string]error
}

func (f fakeChecker) Check(_ context.Context, arn string) (Live, error) {
	if err := f.err[arn]; err != nil {
		return Live{}, err
	}
	if l, ok := f.live[arn]; ok {
		return l, nil
	}
	return Live{Known: true}, nil
}

type fakeStacks struct {
	info map[string]StackInfo
	err  map[string]error
}

func (f fakeStacks) Stack(_ context.Context, name string) (StackInfo, error) {
	if err := f.err[name]; err != nil {
		return StackInfo{Name: name}, err
	}
	i := f.info[name]
	i.Name = name
	return i, nil
}

var (
	// The 2 Nov 2026 incident: four hours after the old lab was destroyed,
	// the tagging API still listed three of its resources.
	staleInstance = arn("ec2", "instance/i-05f7266bbe4e3b48e")
	staleSG       = arn("ec2", "security-group/sg-004c8fc8f7ceab096")
	staleVolume   = arn("ec2", "volume/vol-0a67b371aca9b2a65")
	incident      = []Resource{expiredAs(staleInstance, "cluster"), expiredAs(staleSG, "cluster"), expiredAs(staleVolume, "cluster")}
	goneAll       = fakeChecker{live: map[string]Live{
		staleInstance: {Known: true, Gone: true}, // terminated
		staleSG:       {Known: true, Gone: true}, // InvalidGroup.NotFound
		staleVolume:   {Known: true, Gone: true}, // InvalidVolume.NotFound
	}}
	emptyStacks = fakeStacks{info: map[string]StackInfo{}}
	future      = now.Add(2 * time.Hour)
	past        = now.Add(-time.Hour)
)

func TestConfirmBeforeActing(t *testing.T) {
	karpenter := arn("ec2", "instance/i-karpenter")
	extendedTags := map[string]string{"lab": "true", "component": "karpenter", "expires-at": future.Format(time.RFC3339)}
	tests := []struct {
		name     string
		rs       []Resource
		checker  fakeChecker
		stacks   StackReader
		deletes  []string
		starts   int
		gone     int
		kept     int
		errors   []string // substrings, one per expected error
		teardown string   // substring
	}{
		{
			name: "the incident: a terminated instance, a deleted SG and volume start nothing",
			rs:   incident, checker: goneAll, stacks: emptyStacks,
			gone: 3,
		},
		{
			name:    "live tags win over the tagging API: an extended lab's Karpenter node stays",
			rs:      []Resource{expiredAs(karpenter, "karpenter")},
			checker: fakeChecker{live: map[string]Live{karpenter: {Known: true, Tags: extendedTags}}},
			kept:    1,
		},
		{
			name:    "a failed lookup still counts (fail toward teardown) and is reported",
			rs:      []Resource{expiredAs(karpenter, "karpenter")},
			checker: fakeChecker{err: map[string]error{karpenter: errors.New("Throttling")}},
			deletes: []string{"ec2-instance i-karpenter"},
			errors:  []string{"confirm " + karpenter + ": Throttling"},
		},
		{
			name: "stack gate: state with resources and an expired bundle starts the build",
			rs:   []Resource{expiredAs(arn("eks", "cluster/lab"), "cluster")},
			stacks: fakeStacks{info: map[string]StackInfo{
				"cluster": {Resources: 40, Bundle: true, Expires: past}}},
			starts: 1, teardown: "started build",
		},
		{
			name: "stack gate: a bundle expiring in the future (a new lab) starts nothing",
			rs:   []Resource{expiredAs(arn("eks", "cluster/lab"), "cluster")},
			stacks: fakeStacks{info: map[string]StackInfo{
				"cluster": {Resources: 40, Bundle: true, Expires: future}}},
			teardown: "cluster: bundle expires-at " + future.Format(time.RFC3339) + " is in the future",
		},
		{
			name:     "stack gate: empty states start nothing, and a resource that still exists is a leak",
			rs:       []Resource{expiredAs(arn("eks", "cluster/lab"), "cluster")},
			stacks:   emptyStacks,
			teardown: "cluster: state is empty; registry: state is empty",
			errors:   []string{"1 expired tofu-managed resources still exist but no OpenTofu state holds them"},
		},
		{
			name: "stack gate: state without a bundle cannot be destroyed by the build",
			rs:   []Resource{expiredAs(arn("eks", "cluster/lab"), "cluster")},
			stacks: fakeStacks{info: map[string]StackInfo{
				"cluster": {Resources: 40}}},
			errors: []string{"cluster: 40 resources in its state but no teardown bundle"},
		},
		{
			name:   "stack gate: an unreadable stack counts as due (the build checks the bundle again)",
			rs:     []Resource{expiredAs(arn("eks", "cluster/lab"), "cluster")},
			stacks: fakeStacks{err: map[string]error{"registry": errors.New("AccessDenied")}},
			starts: 1, errors: []string{"registry: AccessDenied (counting it as due)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &fakeDeleter{}
			td := &fakeTeardown{}
			env := Env{Lister: fakeLister{rs: tt.rs}, Deleter: d, Teardown: td, Checker: tt.checker, Stacks: tt.stacks}
			r := run(t, env, Options{})
			if strings.Join(d.got, ",") != strings.Join(tt.deletes, ",") {
				t.Errorf("deletes = %q, want %q", d.got, tt.deletes)
			}
			if td.starts != tt.starts {
				t.Errorf("builds started = %d, want %d (%q)", td.starts, tt.starts, r.Teardown)
			}
			if len(r.Gone) != tt.gone || r.Kept != tt.kept {
				t.Errorf("gone %q kept %d, want %d and %d", r.Gone, r.Kept, tt.gone, tt.kept)
			}
			if len(r.Errors) != len(tt.errors) {
				t.Fatalf("errors = %q, want %q", r.Errors, tt.errors)
			}
			for i, e := range tt.errors {
				if !strings.Contains(r.Errors[i], e) {
					t.Errorf("error %d = %q, want %q", i, r.Errors[i], e)
				}
			}
			if !strings.Contains(r.Teardown, tt.teardown) {
				t.Errorf("teardown = %q, want %q", r.Teardown, tt.teardown)
			}
		})
	}
}

func TestNotFound(t *testing.T) {
	for code, want := range map[string]bool{
		"InvalidInstanceID.NotFound": true, "InvalidGroup.NotFound": true, "InvalidVolume.NotFound": true,
		"InvalidLaunchTemplateId.NotFound": true, "ResourceNotFoundException": true, "LoadBalancerNotFound": true,
		"TargetGroupNotFound": true, "RepositoryNotFoundException": true,
		"AccessDenied": false, "Throttling": false, "InvalidInstanceID.Malformed": false,
	} {
		if got := notFound(&smithy.GenericAPIError{Code: code}); got != want {
			t.Errorf("notFound(%s) = %v", code, got)
		}
	}
	if notFound(nil) || notFound(errors.New("NotFound")) {
		t.Error("only API errors count")
	}
}

func bundle(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// S3Stacks against S3 as the reaper sees it: 403 for a missing key unless
// a listing of that key is allowed.
func TestS3Stacks(t *testing.T) {
	const live = `{"version":4,"resources":[{"mode":"data","type":"aws_caller_identity"},{"mode":"managed","type":"aws_vpc"},{"mode":"managed","type":"aws_eks_cluster"}]}`
	const destroyed = `{"version":4,"resources":[],"check_results":[]}`
	b := bundle(t, map[string]string{"main.tf": "", BundleVarsFile: `{"owner":"x","expires_at":"2026-11-02T18:34:00Z"}`})
	f := &fakeS3{listPrefixes: []string{"cluster/", "teardown/"}, objects: map[string][]byte{
		ClusterStateKey: []byte(live), RegistryStateKey: []byte(destroyed), BundleKey("cluster"): b,
	}}
	s := S3Stacks{Client: f, Bucket: "state"}
	c, err := s.Stack(context.Background(), "cluster")
	if err != nil || c.Resources != 2 || !c.Bundle || !c.Expires.Equal(time.Date(2026, 11, 2, 18, 34, 0, 0, time.UTC)) {
		t.Errorf("cluster = %+v, %v", c, err)
	}
	r, err := s.Stack(context.Background(), "registry")
	if err != nil || r.Resources != 0 || r.Bundle {
		t.Errorf("registry = %+v, %v", r, err)
	}
	delete(f.objects, RegistryStateKey)
	if r, err := s.Stack(context.Background(), "registry"); err != nil || r.Resources != 0 {
		t.Errorf("registry with no state (403, then listed as missing) = %+v, %v", r, err)
	}
	f.objects[BundleKey("cluster")] = bundle(t, map[string]string{"main.tf": ""})
	if _, err := s.Stack(context.Background(), "cluster"); err == nil {
		t.Error("a bundle without its variables file must be an error, not 'not expired'")
	}
}

// A terminated instance is gone: DescribeInstances lists it for about an
// hour after (i-05f7266bbe4e3b48e on 2 Nov 2026 was one).
func TestInstanceLive(t *testing.T) {
	inst := func(state ec2types.InstanceStateName) *ec2.DescribeInstancesOutput {
		return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{{
			State: &ec2types.InstanceState{Name: state},
			Tags:  []ec2types.Tag{{Key: aws.String("expires-at"), Value: aws.String("2026-11-02T19:00:00Z")}},
		}}}}}
	}
	if l := instanceLive(inst(ec2types.InstanceStateNameTerminated)); !l.Gone {
		t.Errorf("terminated: %+v", l)
	}
	if l := instanceLive(inst(ec2types.InstanceStateNameRunning)); l.Gone || l.Tags["expires-at"] != "2026-11-02T19:00:00Z" {
		t.Errorf("running: %+v", l)
	}
	if l := instanceLive(&ec2.DescribeInstancesOutput{}); !l.Gone {
		t.Errorf("not listed: %+v", l)
	}
}

func TestSpotRequestAndFleetLive(t *testing.T) {
	tags := []ec2types.Tag{{Key: aws.String("lab"), Value: aws.String("true")}}
	spot := func(s ec2types.SpotInstanceState) *ec2.DescribeSpotInstanceRequestsOutput {
		return &ec2.DescribeSpotInstanceRequestsOutput{SpotInstanceRequests: []ec2types.SpotInstanceRequest{{State: s, Tags: tags}}}
	}
	fleet := func(typ ec2types.FleetType, st ec2types.FleetStateCode) *ec2.DescribeFleetsOutput {
		return &ec2.DescribeFleetsOutput{Fleets: []ec2types.FleetData{{Type: typ, FleetState: st, Tags: tags}}}
	}
	tests := []struct {
		name string
		live Live
		gone bool
	}{
		{"spot closed", spotRequestLive(spot(ec2types.SpotInstanceStateClosed)), true},
		{"spot cancelled", spotRequestLive(spot(ec2types.SpotInstanceStateCancelled)), true},
		{"spot active", spotRequestLive(spot(ec2types.SpotInstanceStateActive)), false},
		{"spot missing", spotRequestLive(&ec2.DescribeSpotInstanceRequestsOutput{}), true},
		{"instant fleet", fleetLive(fleet(ec2types.FleetTypeInstant, ec2types.FleetStateCodeActive)), true},
		{"maintain fleet", fleetLive(fleet(ec2types.FleetTypeMaintain, ec2types.FleetStateCodeActive)), false},
		{"deleted fleet", fleetLive(fleet(ec2types.FleetTypeMaintain, ec2types.FleetStateCodeDeleted)), true},
		{"fleet missing", fleetLive(&ec2.DescribeFleetsOutput{}), true},
	}
	for _, tt := range tests {
		if !tt.live.Known || tt.live.Gone != tt.gone {
			t.Errorf("%s: live = %+v, want gone %v", tt.name, tt.live, tt.gone)
		}
		if !tt.gone && tt.live.Tags["lab"] != "true" {
			t.Errorf("%s: tags = %v", tt.name, tt.live.Tags)
		}
	}
}
