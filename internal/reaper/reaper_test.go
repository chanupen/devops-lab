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

type fakeTeardown struct {
	starts  int
	running string
	builds  map[string]Build
	err     error
	last    [2]int // attempt, max of the last Start
}

func (f *fakeTeardown) Running(context.Context) (string, error) { return f.running, nil }

func (f *fakeTeardown) Get(_ context.Context, id string) (Build, error) {
	b, ok := f.builds[id]
	if !ok {
		return Build{ID: id, State: "FAILED", End: now.Add(-20 * time.Minute)}, nil
	}
	return b, nil
}

func (f *fakeTeardown) Start(_ context.Context, attempt, maxAttempts int) (string, error) {
	f.starts++
	f.last = [2]int{attempt, maxAttempts}
	if f.err != nil {
		return "", f.err
	}
	return fmt.Sprintf("lab-teardown:%d", f.starts), nil
}

type fakeStore struct {
	st      TeardownState
	cleared bool
}

func (f *fakeStore) Load(context.Context) (TeardownState, error) { return f.st, nil }
func (f *fakeStore) Save(_ context.Context, st TeardownState) error {
	f.st = st
	return nil
}
func (f *fakeStore) Clear(context.Context) error {
	f.st, f.cleared = TeardownState{}, true
	return nil
}

type fakeNotify struct{ subjects, bodies []string }

func (f *fakeNotify) Notify(_ context.Context, subject, body string) error {
	f.subjects, f.bodies = append(f.subjects, subject), append(f.bodies, body)
	return nil
}

type fakeVPCs map[string][]Target

func (f fakeVPCs) InVPC(_ context.Context, id string) ([]Target, error) { return f[id], nil }

func run(t *testing.T, env Env, opts Options) Result {
	t.Helper()
	if env.Lister == nil {
		env.Lister = fakeLister{}
	}
	if env.Deleter == nil {
		env.Deleter = &fakeDeleter{}
	}
	r, err := Run(context.Background(), env, now, opts, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func expiredAs(a, component string, extra ...string) Resource {
	tags := append([]string{"lab", "true", "expires-at", now.Add(-time.Hour).Format(time.RFC3339), "component", component}, extra...)
	return res(a, tags...)
}

func TestRun(t *testing.T) {
	nlb := arn("elasticloadbalancing", "loadbalancer/net/k8s-podinfo/1")
	tg := arn("elasticloadbalancing", "targetgroup/k8s-podinfo/2")
	tests := []struct {
		name      string
		rs        []Resource
		opts      Options
		fail      map[string]bool
		wait      map[string]bool
		teardown  *fakeTeardown
		deletes   []string // calls to the deleter, in order
		reports   []string // Result.Deleted as "kind id"
		managed   int
		starts    int // teardown builds started
		kept      int
		skipped   int
		waiting   int
		errors    int
		teardownS string
	}{
		{
			// A cloud lab past its TTL, as the tagging API lists it (any
			// order). Controller-made resources are deleted here, in order;
			// everything tofu created goes to one teardown build.
			name: "an expired EKS lab: controller resources deleted in order, the rest to the teardown",
			rs: []Resource{
				expiredAs(arn("eks", "cluster/lab"), "cluster"),
				expired(arn("ec2", "volume/vol-pvc")),
				expiredAs(nlb, "load-balancer-controller"),
				expiredAs(arn("ec2", "security-group/sg-lbc"), "load-balancer-controller"),
				expiredAs(arn("ec2", "instance/i-karpenter-1"), "karpenter", "karpenter.sh/nodepool", "spot"),
				expiredAs(arn("ec2", "instance/i-system"), "cluster", NodeGroupTag, "system"),
				expiredAs(arn("eks", "nodegroup/lab/system/uuid-1"), "cluster"),
				expiredAs(tg, "load-balancer-controller"),
				expiredAs(arn("ec2", "instance/i-karpenter-2"), "karpenter", "karpenter.sh/nodepool", "spot"),
				expiredAs(arn("ec2", "vpc/vpc-1"), "cluster"),                                         // a type the reaper cannot delete: tofu can
				expiredAs(arn("ecr", "repository/lab"), "registry"),                                   // the registry module
				res(arn("s3", "lab-tofu-state-111122223333"), "lab", "true", "component", "baseline"), // permanent
			},
			teardown: &fakeTeardown{},
			deletes: []string{
				"ec2-instance i-karpenter-1", "ec2-instance i-karpenter-2",
				"load-balancer " + nlb, "target-group " + tg, "security-group sg-lbc",
				"ebs-volume vol-pvc",
			},
			managed: 5, starts: 1, kept: 1, teardownS: "started build lab-teardown:1 (attempt 1 of 6)",
		},
		{
			name:     "a running build is not started twice",
			rs:       []Resource{expiredAs(arn("eks", "cluster/lab"), "cluster")},
			teardown: &fakeTeardown{running: "lab-teardown:1"},
			managed:  1, teardownS: "build lab-teardown:1 already running (attempt 1 of 6)",
		},
		{
			name:     "sweep only (the build asking) never starts a build",
			rs:       []Resource{expiredAs(arn("eks", "cluster/lab"), "cluster"), expiredAs(arn("ec2", "instance/i-k"), "karpenter")},
			opts:     Options{SweepOnly: true},
			teardown: &fakeTeardown{},
			deletes:  []string{"ec2-instance i-k"},
			managed:  1,
		},
		{
			name:     "a failed start is an error",
			rs:       []Resource{expiredAs(arn("eks", "cluster/lab"), "cluster")},
			teardown: &fakeTeardown{err: errors.New("AccessDenied")},
			managed:  1, starts: 1, errors: 1,
		},
		{
			name:     "dry run deletes nothing, starts nothing and reports what it would",
			rs:       []Resource{expired(arn("ec2", "instance/i-1")), expired(arn("ec2", "volume/vol-1")), expiredAs(arn("eks", "cluster/lab"), "cluster")},
			opts:     Options{DryRun: true},
			teardown: &fakeTeardown{},
			reports:  []string{"ec2-instance i-1", "ebs-volume vol-1"},
			managed:  1, teardownS: "dry run: would start the teardown build",
		},
		{
			name:    "a security group still in use waits for the next run",
			rs:      []Resource{expired(arn("ec2", "security-group/sg-1")), expired(arn("elasticloadbalancing", "loadbalancer/app/web/abc"))},
			wait:    map[string]bool{"sg-1": true},
			deletes: []string{"load-balancer " + arn("elasticloadbalancing", "loadbalancer/app/web/abc"), "security-group sg-1"},
			reports: []string{"load-balancer " + arn("elasticloadbalancing", "loadbalancer/app/web/abc")},
			waiting: 1,
		},
		{
			name:    "expires-at exactly now is expired",
			rs:      []Resource{res(arn("ec2", "instance/i-1"), "lab", "true", "expires-at", now.Format(time.RFC3339))},
			deletes: []string{"ec2-instance i-1"},
		},
		{
			name: "bad expires-at and unknown unmanaged types are skipped, never deleted",
			rs: []Resource{
				res(arn("ec2", "instance/i-1"), "lab", "true", "expires-at", "tomorrow"),
				expired(arn("rds", "db:lab")),
				expired(arn("ec2", "vpc/vpc-stray")), // no tofu component: swept, never deleted
			},
			skipped: 3,
		},
		{
			name: "without lab=true, or not yet expired, it is kept",
			rs: []Resource{
				res(arn("ec2", "instance/i-1"), "lab", "false", "expires-at", now.Add(-time.Hour).Format(time.RFC3339)),
				res(arn("ec2", "instance/i-2"), "lab", "true", "expires-at", now.Add(time.Hour).Format(time.RFC3339)),
			},
			kept: 2,
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
			var td Teardown
			if tt.teardown != nil {
				td = tt.teardown
			}
			r, err := Run(context.Background(), Env{Lister: fakeLister{rs: tt.rs}, Deleter: d, Teardown: td}, now, tt.opts, func(f string, a ...any) {
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
			starts := 0
			if tt.teardown != nil {
				starts = tt.teardown.starts
			}
			if len(r.Managed) != tt.managed || starts != tt.starts || r.Teardown != tt.teardownS {
				t.Errorf("managed %d starts %d teardown %q; want %d %d %q", len(r.Managed), starts, r.Teardown, tt.managed, tt.starts, tt.teardownS)
			}
			if r.Kept != tt.kept || len(r.Skipped) != tt.skipped || len(r.Waiting) != tt.waiting || len(r.Errors) != tt.errors || r.DryRun != tt.opts.DryRun {
				t.Errorf("kept %d skipped %d waiting %d errors %d dry %v; want %d %d %d %d %v", r.Kept, len(r.Skipped), len(r.Waiting),
					len(r.Errors), r.DryRun, tt.kept, tt.skipped, tt.waiting, tt.errors, tt.opts.DryRun)
			}
			if tt.opts.DryRun && (len(logs) == 0 || !strings.HasPrefix(logs[0], "dry run: would delete ec2-instance i-1 (expired 1h0m0s ago)")) {
				t.Errorf("dry-run log = %q", logs)
			}
		})
	}
}

func TestRunListError(t *testing.T) {
	_, err := Run(context.Background(), Env{Lister: fakeLister{err: errors.New("AccessDenied")}, Deleter: &fakeDeleter{}}, now, Options{}, func(string, ...any) {})
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
		{arn("elasticloadbalancing", "loadbalancer/old"), ClassicLB, "old", false},
		{arn("elasticloadbalancing", "loadbalancer/"), "", "", true},
		{arn("ec2", "network-interface/eni-1"), NetworkInterface, "eni-1", false},
		{arn("ec2", "security-group/sg-1"), SecurityGroup, "sg-1", false},
		{arn("elasticloadbalancing", "targetgroup/k8s-a/1"), TargetGroup, arn("elasticloadbalancing", "targetgroup/k8s-a/1"), false},
		{arn("ec2", "vpc/vpc-1"), VPC, "vpc-1", false},
		{"not-an-arn", "", "", true},
	}
	for _, tt := range tests {
		kind, id, err := parseARN(tt.arn)
		if (err != nil) != tt.err || kind != tt.kind || id != tt.id {
			t.Errorf("parseARN(%q) = %q %q %v; want %q %q err %v", tt.arn, kind, id, err, tt.kind, tt.id, tt.err)
		}
	}
}

// An untagged Classic Load Balancer (a plain type: LoadBalancer Service)
// and its network interface and security group sit in the lab VPC; the
// tagging API does not list them, but the VPC sweep finds and deletes
// them, in order, once with a tagged one already in the list.
func TestRunSweepsTheLabVPC(t *testing.T) {
	nlb := arn("elasticloadbalancing", "loadbalancer/net/k8s-a/1")
	d := &fakeDeleter{wait: map[string]bool{"sg-elb": true}}
	vpcs := fakeVPCs{"vpc-1": {
		{Kind: SecurityGroup, ID: "sg-elb"},
		{Kind: NetworkInterface, ID: "eni-free"},
		{Kind: ClassicLB, ID: "a1b2c3"},
		{Kind: LoadBalancer, ARN: nlb, ID: nlb}, // also tagged: deleted once
		{Kind: TargetGroup, ID: "tg-untagged"},
	}}
	rs := []Resource{expiredAs(arn("ec2", "vpc/vpc-1"), "cluster"), expiredAs(nlb, "load-balancer-controller")}
	r := run(t, Env{Lister: fakeLister{rs: rs}, Deleter: d, VPCs: vpcs, Teardown: &fakeTeardown{}}, Options{SweepOnly: true})
	want := []string{"load-balancer " + nlb, "classic-load-balancer a1b2c3", "target-group tg-untagged",
		"network-interface eni-free", "security-group sg-elb"}
	if !slices.Equal(d.got, want) {
		t.Errorf("deletes = %q\nwant      %q", d.got, want)
	}
	if len(r.Waiting) != 1 || len(r.Managed) != 1 || r.Managed[0] != "vpc vpc-1" {
		t.Errorf("waiting %q managed %q", r.Waiting, r.Managed)
	}
	for _, x := range r.Deleted {
		if !x.Expires.Equal(now.Add(-time.Hour)) {
			t.Errorf("%s %s: expires %s, want the VPC's", x.Kind, x.ID, x.Expires)
		}
	}
	// A VPC that has not expired is not swept.
	d = &fakeDeleter{}
	live := res(arn("ec2", "vpc/vpc-1"), "lab", "true", "component", "cluster", "expires-at", now.Add(time.Hour).Format(time.RFC3339))
	run(t, Env{Lister: fakeLister{rs: []Resource{live}}, Deleter: d, VPCs: vpcs}, Options{})
	if len(d.got) != 0 {
		t.Errorf("live VPC swept: %q", d.got)
	}
}

func TestTeardownRetriesThenAsksForHelp(t *testing.T) {
	lab := []Resource{expiredAs(arn("eks", "cluster/lab"), "cluster")}
	td := &fakeTeardown{builds: map[string]Build{}}
	store := &fakeStore{}
	notify := &fakeNotify{}
	env := Env{Lister: fakeLister{rs: lab}, Teardown: td, Store: store, Notify: notify}

	for i := 1; i <= MaxAttempts; i++ {
		r := run(t, env, Options{})
		if want := fmt.Sprintf("started build lab-teardown:%d (attempt %d of 6)", i, i); r.Teardown != want {
			t.Fatalf("run %d: %q, want %q", i, r.Teardown, want)
		}
		if td.last != [2]int{i, MaxAttempts} {
			t.Errorf("run %d: build told attempt %v", i, td.last)
		}
		// While it runs, nothing new starts.
		td.running = store.st.LastBuild
		if r := run(t, env, Options{}); !strings.Contains(r.Teardown, "already running") || td.starts != i {
			t.Errorf("run %d while running: %q, %d starts", i, r.Teardown, td.starts)
		}
		td.running = "" // it failed (the fake's default)
	}
	r := run(t, env, Options{})
	if !r.ManualAction || td.starts != MaxAttempts || len(notify.subjects) != 1 || notify.subjects[0] != "lab teardown: manual action needed" {
		t.Fatalf("after %d attempts: manual %v, %d starts, mails %q", MaxAttempts, r.ManualAction, td.starts, notify.subjects)
	}
	if b := notify.bodies[0]; !strings.Contains(b, "eks-cluster lab") || !strings.Contains(b, "lab down --mode cloud") {
		t.Errorf("manual-action mail:\n%s", b)
	}
	if r := run(t, env, Options{}); !r.ManualAction || len(notify.subjects) != 1 {
		t.Errorf("mailed again: %q", notify.subjects)
	}
	// Once nothing expired is left, the state goes; the next lab starts at 1.
	run(t, Env{Lister: fakeLister{}, Teardown: td, Store: store}, Options{})
	if !store.cleared || store.st.Attempts != 0 {
		t.Errorf("state not cleared: %+v", store.st)
	}
}

func TestTeardownNewLabStartsAtOne(t *testing.T) {
	td := &fakeTeardown{}
	store := &fakeStore{st: TeardownState{Lab: "2026-10-01T00:00:00Z", Attempts: MaxAttempts, Notified: true, LastBuild: "old"}}
	r := run(t, Env{Lister: fakeLister{rs: []Resource{expiredAs(arn("eks", "cluster/lab"), "cluster")}}, Teardown: td, Store: store}, Options{})
	if r.ManualAction || td.last != [2]int{1, MaxAttempts} || store.st.Lab != now.Add(-time.Hour).Format(time.RFC3339) {
		t.Errorf("new lab: %q, attempt %v, state %+v", r.Teardown, td.last, store.st)
	}
}

func TestTeardownWaitsAfterASuccess(t *testing.T) {
	lab := []Resource{expiredAs(arn("eks", "cluster/lab"), "cluster")}
	exp := now.Add(-time.Hour).Format(time.RFC3339)
	for _, tt := range []struct {
		ago    time.Duration
		starts int
	}{{10 * time.Minute, 0}, {SettleAfterSuccess + time.Minute, 1}} {
		td := &fakeTeardown{builds: map[string]Build{"b1": {ID: "b1", State: BuildSucceeded, End: now.Add(-tt.ago)}}}
		store := &fakeStore{st: TeardownState{Lab: exp, Attempts: 1, LastBuild: "b1"}}
		r := run(t, Env{Lister: fakeLister{rs: lab}, Teardown: td, Store: store}, Options{})
		if td.starts != tt.starts {
			t.Errorf("succeeded %s ago: %d starts (%q), want %d", tt.ago, td.starts, r.Teardown, tt.starts)
		}
	}
}

func TestDryRunLeavesTeardownStateAlone(t *testing.T) {
	store := &fakeStore{st: TeardownState{Lab: "x", Attempts: 2}}
	run(t, Env{Lister: fakeLister{}, Store: store}, Options{DryRun: true})
	run(t, Env{Lister: fakeLister{}, Store: store}, Options{SweepOnly: true})
	if store.cleared || store.st.Attempts != 2 {
		t.Errorf("state changed: %+v", store.st)
	}
}
