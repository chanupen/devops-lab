// Package reaper is the cloud lab's dead man's switch. Every 15 minutes it
// runs as a Lambda (cmd/reaper) and, for lab=true resources whose
// expires-at has passed:
//
//   - deletes what the in-cluster controllers created and OpenTofu does not
//     know about: Karpenter's instances, load balancers and target groups
//     from the Load Balancer Controller, their security groups, volumes;
//   - inside an expired lab VPC, also deletes every load balancer (Classic
//     ones too), target group, unattached network interface and
//     non-default security group, tagged or not: anything in the lab VPC
//     belongs to the lab, and any of them would block the VPC's destroy;
//   - starts the teardown build (CodeBuild), which runs tofu destroy on the
//     cluster and registry state, sweeps again and emails a summary, for
//     everything OpenTofu manages (component=cluster or registry). While
//     lab resources remain it starts the build again, at most MaxAttempts
//     times, then emails "manual action needed".
//
// Resources with no expires-at (the account baseline) are always kept.
package reaper

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Tag keys.
const (
	LabTag       = "lab"
	ExpiresTag   = "expires-at"
	ComponentTag = "component"
)

// ManagedComponents are the components OpenTofu owns. The reaper never
// deletes them one by one: the teardown build destroys them with tofu, so
// the state stays true and dependencies go in the right order.
var ManagedComponents = []string{"cluster", "registry"}

// Kind is a resource type the reaper knows how to delete.
type Kind string

// Kinds, in the order the reaper deletes them: instances first (Karpenter's
// nodes), then load balancers, the target groups, network interfaces and
// security groups they used, and last the volumes and addresses, free by
// then. Node groups and clusters are tofu-managed in the lab and normally
// go through the teardown build; they are here for strays. VPCs are never
// deleted here: an expired one makes the reaper sweep what is inside it.
const (
	NodeGroup        Kind = "eks-nodegroup"
	Instance         Kind = "ec2-instance"
	LoadBalancer     Kind = "load-balancer"
	ClassicLB        Kind = "classic-load-balancer"
	TargetGroup      Kind = "target-group"
	NetworkInterface Kind = "network-interface"
	SecurityGroup    Kind = "security-group"
	Cluster          Kind = "eks-cluster"
	Volume           Kind = "ebs-volume"
	Address          Kind = "elastic-ip"
	VPC              Kind = "vpc"
)

var order = map[Kind]int{NodeGroup: 0, Instance: 1, LoadBalancer: 2, ClassicLB: 3, TargetGroup: 4, NetworkInterface: 5,
	SecurityGroup: 6, Cluster: 7, Volume: 8, Address: 9}

// MaxAttempts is how many teardown builds the reaper starts for one
// expired lab before it stops and emails "manual action needed".
const MaxAttempts = 6

// SettleAfterSuccess is how long the reaper waits after a teardown build
// succeeded before it starts another for resources the tagging API still
// lists: that API can lag behind deletions.
const SettleAfterSuccess = time.Hour

// ErrNotYet is returned by a Deleter when a resource cannot go yet: a
// cluster whose node groups are still deleting, a security group a load
// balancer still uses. The run reports it as waiting, not failed; the next
// run (15 minutes later) tries again.
var ErrNotYet = errors.New("not yet")

// NodeGroupTag is set by EKS on the instances of a managed node group.
// Deleting the node group removes them, so the reaper leaves them to it.
const NodeGroupTag = "eks:nodegroup-name"

// Resource is one tagged resource as the tagging API reports it.
type Resource struct {
	ARN  string
	Tags map[string]string
}

// Target is a resource the reaper will delete, or leave to the teardown.
type Target struct {
	Kind    Kind
	ARN     string
	ID      string // instance, volume, group or allocation ID; "cluster/nodegroup"; cluster name; the ARN for ELB resources
	Expires time.Time
	Managed bool // OpenTofu owns it: the teardown build destroys it
}

// Lister finds resources tagged lab=true.
type Lister interface {
	List(ctx context.Context) ([]Resource, error)
}

// Deleter deletes one target.
type Deleter interface {
	Delete(ctx context.Context, t Target) error
}

// VPCScanner lists what sits inside a VPC, tagged or not: load balancers
// (Classic too), target groups, unattached network interfaces and
// security groups other than the default one.
type VPCScanner interface {
	InVPC(ctx context.Context, vpcID string) ([]Target, error)
}

// Build states as CodeBuild reports them.
const (
	BuildInProgress = "IN_PROGRESS"
	BuildSucceeded  = "SUCCEEDED"
)

// Build is one teardown build.
type Build struct {
	ID    string
	State string // IN_PROGRESS, SUCCEEDED, FAILED, FAULT, TIMED_OUT, STOPPED
	End   time.Time
}

// Teardown runs the build that runs tofu destroy.
type Teardown interface {
	// Running is the ID of a build in progress, or "".
	Running(ctx context.Context) (string, error)
	// Get is one build's state.
	Get(ctx context.Context, id string) (Build, error)
	// Start starts a build, telling it which attempt it is.
	Start(ctx context.Context, attempt, maxAttempts int) (id string, err error)
}

// TeardownState is what the reaper remembers between runs about the
// teardown of one expired lab (stored next to the heartbeat).
type TeardownState struct {
	// Lab is the earliest expires-at among the expired tofu-managed
	// resources: a new lab has a later one and starts again at attempt 1.
	Lab       string    `json:"lab"`
	Attempts  int       `json:"attempts"`
	LastBuild string    `json:"lastBuild,omitempty"`
	Notified  bool      `json:"manualActionNotified,omitempty"`
	Updated   time.Time `json:"updated"`
}

// TeardownStateKey is where TeardownState lives in the state bucket.
const TeardownStateKey = "reaper/teardown.json"

// StateStore keeps TeardownState.
type StateStore interface {
	Load(ctx context.Context) (TeardownState, error) // zero value when there is none
	Save(ctx context.Context, st TeardownState) error
	Clear(ctx context.Context) error
}

// Notifier emails the owner (SNS lab-teardown).
type Notifier interface {
	Notify(ctx context.Context, subject, body string) error
}

// Env is what a run talks to. VPCs, Teardown, Store and Notify may be nil:
// that part is then skipped (Teardown nil with expired tofu-managed
// resources is an error).
type Env struct {
	Lister   Lister
	Deleter  Deleter
	VPCs     VPCScanner
	Teardown Teardown
	Store    StateStore
	Notify   Notifier
}

// Options change what a run does.
type Options struct {
	DryRun bool // only report
	// SweepOnly deletes controller-created resources but never starts the
	// teardown: the teardown build itself asks for sweeps.
	SweepOnly bool
}

// Result is what one run did.
type Result struct {
	DryRun   bool
	Deleted  []Target // or, in dry run, would delete
	Managed  []string // expired and tofu-managed: left to the teardown build
	Teardown string   // what happened to the teardown build, if one was needed
	Kept     int      // tagged lab=true but not expired, or no expires-at
	Skipped  []string // could not be handled: unknown type or bad expires-at
	Waiting  []string // expired, but must wait for something else to go first
	Errors   []string
	// ManualAction is set once MaxAttempts teardown builds did not clear
	// the lab: the reaper starts no more and has emailed the owner.
	ManualAction bool
}

// Run finds expired lab resources, deletes the controller-created ones and
// whatever sits in an expired lab VPC, and starts the teardown for the
// rest, or only reports when opts.DryRun is set. A failed delete is
// reported and the run goes on.
func Run(ctx context.Context, env Env, now time.Time, opts Options, logf func(string, ...any)) (Result, error) {
	res := Result{DryRun: opts.DryRun}
	found, err := env.Lister.List(ctx)
	if err != nil {
		return res, fmt.Errorf("list lab resources: %w", err)
	}
	var targets, vpcs []Target
	var lab time.Time // earliest expiry among tofu-managed targets
	for _, r := range found {
		t, why := classify(r, now)
		switch {
		case errors.Is(why, errKeep):
			res.Kept++
			continue
		case why != nil:
			res.Skipped = append(res.Skipped, r.ARN+": "+why.Error())
			logf("skip %s: %v", r.ARN, why)
			continue
		}
		if t.Kind == VPC {
			vpcs = append(vpcs, t)
		}
		switch {
		case t.Managed:
			res.Managed = append(res.Managed, strings.TrimSpace(string(t.Kind)+" "+t.ID))
			if lab.IsZero() || t.Expires.Before(lab) {
				lab = t.Expires
			}
		case t.Kind == VPC:
			res.Skipped = append(res.Skipped, r.ARN+": a VPC without a tofu component is swept but not deleted")
		default:
			targets = append(targets, t)
		}
	}
	targets = append(targets, scanVPCs(ctx, env.VPCs, vpcs, targets, &res, logf)...)
	sort.SliceStable(targets, func(i, j int) bool { return order[targets[i].Kind] < order[targets[j].Kind] })
	for _, t := range targets {
		late := now.Sub(t.Expires).Round(time.Minute)
		if opts.DryRun {
			logf("dry run: would delete %s %s (expired %s ago)", t.Kind, t.ID, late)
			res.Deleted = append(res.Deleted, t)
			continue
		}
		err := env.Deleter.Delete(ctx, t)
		if errors.Is(err, ErrNotYet) {
			res.Waiting = append(res.Waiting, fmt.Sprintf("%s %s: %v", t.Kind, t.ID, err))
			logf("wait %s %s: %v", t.Kind, t.ID, err)
			continue
		}
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s %s: %v", t.Kind, t.ID, err))
			logf("delete %s %s failed: %v", t.Kind, t.ID, err)
			continue
		}
		logf("deleted %s %s (expired %s ago)", t.Kind, t.ID, late)
		res.Deleted = append(res.Deleted, t)
	}
	if opts.SweepOnly {
		return res, nil
	}
	switch {
	case len(res.Managed) > 0 && opts.DryRun:
		res.Teardown = "dry run: would start the teardown build"
	case len(res.Managed) > 0:
		runTeardown(ctx, env, lab, now, &res)
	case !opts.DryRun && env.Store != nil:
		// Nothing expired is left: the next expired lab starts at attempt 1.
		if err := env.Store.Clear(ctx); err != nil {
			res.Errors = append(res.Errors, "teardown state: "+err.Error())
		}
	}
	if len(res.Managed) > 0 {
		logf("%d expired tofu-managed resources; teardown: %s", len(res.Managed), orNone(res.Teardown))
	}
	return res, nil
}

// scanVPCs lists what sits inside each expired VPC and is not a target
// already. Everything found takes the VPC's expiry.
func scanVPCs(ctx context.Context, s VPCScanner, vpcs, have []Target, res *Result, logf func(string, ...any)) []Target {
	if s == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, t := range have {
		seen[string(t.Kind)+" "+t.ID] = true
	}
	var out []Target
	for _, v := range vpcs {
		in, err := s.InVPC(ctx, v.ID)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("scan %s: %v", v.ID, err))
			logf("scan %s failed: %v", v.ID, err)
			continue
		}
		for _, t := range in {
			if key := string(t.Kind) + " " + t.ID; !seen[key] {
				seen[key] = true
				t.Expires = v.Expires
				out = append(out, t)
			}
		}
	}
	return out
}

// runTeardown starts the teardown build when none is running, at most
// MaxAttempts times for one lab, and emails "manual action needed" once
// those are used up.
func runTeardown(ctx context.Context, env Env, lab, now time.Time, res *Result) {
	fail := func(what string, err error) { res.Errors = append(res.Errors, what+": "+err.Error()) }
	if env.Teardown == nil {
		res.Errors = append(res.Errors, "teardown: no teardown build configured")
		return
	}
	var st TeardownState
	if env.Store != nil {
		var err error
		if st, err = env.Store.Load(ctx); err != nil {
			fail("teardown state", err)
			return
		}
	}
	if id := lab.UTC().Format(time.RFC3339); st.Lab != id {
		st = TeardownState{Lab: id}
	}
	running, err := env.Teardown.Running(ctx)
	if err != nil {
		fail("teardown", err)
		return
	}
	if running != "" {
		res.Teardown = fmt.Sprintf("build %s already running (attempt %d of %d)", running, max(st.Attempts, 1), MaxAttempts)
		return
	}
	if st.LastBuild != "" {
		b, err := env.Teardown.Get(ctx, st.LastBuild)
		if err != nil {
			fail("teardown", err)
			return
		}
		if b.State == BuildSucceeded && now.Sub(b.End) < SettleAfterSuccess {
			res.Teardown = fmt.Sprintf("build %s succeeded %s ago; waiting up to %s for the tagging API before another attempt",
				b.ID, now.Sub(b.End).Round(time.Minute), SettleAfterSuccess)
			return
		}
	}
	save := func() {
		st.Updated = now
		if env.Store != nil {
			if err := env.Store.Save(ctx, st); err != nil {
				fail("teardown state", err)
			}
		}
	}
	if st.Attempts >= MaxAttempts {
		res.ManualAction = true
		res.Teardown = fmt.Sprintf("manual action needed: %d teardown builds did not clear the lab", st.Attempts)
		if st.Notified || env.Notify == nil {
			return
		}
		if err := env.Notify.Notify(ctx, "lab teardown: manual action needed", manualBody(st, res.Managed)); err != nil {
			fail("notify", err)
			return
		}
		st.Notified = true
		save()
		return
	}
	id, err := env.Teardown.Start(ctx, st.Attempts+1, MaxAttempts)
	if err != nil {
		fail("teardown", err)
		return
	}
	st.Attempts++
	st.LastBuild = id
	res.Teardown = fmt.Sprintf("started build %s (attempt %d of %d)", id, st.Attempts, MaxAttempts)
	save()
}

func manualBody(st TeardownState, managed []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The reaper started %d teardown builds for the cloud lab that expired at %s, and lab resources still remain.\n", st.Attempts, st.Lab)
	fmt.Fprintf(&b, "It starts no more builds for this lab. They may still bill.\n\nLast build: %s\n\nStill there (expired, managed by OpenTofu):\n", st.LastBuild)
	for _, m := range managed {
		fmt.Fprintf(&b, "  %s\n", m)
	}
	b.WriteString("\nWhat to do:\n")
	b.WriteString("  1. Read the last build's log (CodeBuild project lab-teardown, log group /aws/codebuild/lab-teardown).\n")
	b.WriteString("  2. On the lab host: lab down --mode cloud, then lab cloud leftovers (it exits non-zero while anything is left).\n")
	fmt.Fprintf(&b, "  3. To let the reaper try %d more times instead, delete %s from the state bucket.\n", MaxAttempts, TeardownStateKey)
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "not started"
	}
	return s
}

var errKeep = errors.New("keep")

// classify turns a tagged resource into a target, errKeep when it must
// stay, or another error when it cannot be handled.
func classify(r Resource, now time.Time) (Target, error) {
	if r.Tags[LabTag] != "true" {
		return Target{}, errKeep // the lister filters on lab=true; be safe anyway
	}
	raw, ok := r.Tags[ExpiresTag]
	if !ok || raw == "" {
		return Target{}, errKeep // permanent, e.g. the baseline
	}
	exp, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return Target{}, fmt.Errorf("expires-at %q is not RFC3339", raw)
	}
	if now.Before(exp) {
		return Target{}, errKeep
	}
	managed := slices.Contains(ManagedComponents, r.Tags[ComponentTag])
	kind, id, err := parseARN(r.ARN)
	if err != nil {
		if managed {
			// tofu destroys every type it created, known here or not.
			return Target{ARN: r.ARN, ID: shortARN(r.ARN), Expires: exp, Managed: true}, nil
		}
		return Target{}, err
	}
	if kind == Instance && r.Tags[NodeGroupTag] != "" && !managed {
		return Target{}, errKeep // its node group's deletion removes it
	}
	return Target{Kind: kind, ARN: r.ARN, ID: id, Expires: exp, Managed: managed}, nil
}

func shortARN(arn string) string {
	p := strings.SplitN(arn, ":", 6)
	if len(p) != 6 {
		return arn
	}
	return p[2] + ":" + p[5]
}

// parseARN maps an ARN to a kind and the ID its delete call takes.
func parseARN(arn string) (Kind, string, error) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" {
		return "", "", fmt.Errorf("not an ARN")
	}
	service, resource := parts[2], parts[5]
	typ, rest, _ := strings.Cut(resource, "/")
	switch {
	case service == "ec2" && typ == "instance":
		return Instance, rest, nil
	case service == "ec2" && typ == "volume":
		return Volume, rest, nil
	case service == "ec2" && typ == "elastic-ip":
		return Address, rest, nil
	case service == "ec2" && typ == "security-group":
		return SecurityGroup, rest, nil
	case service == "ec2" && typ == "network-interface":
		return NetworkInterface, rest, nil
	case service == "ec2" && typ == "vpc":
		return VPC, rest, nil
	case service == "eks" && typ == "cluster":
		if rest == "" || strings.Contains(rest, "/") {
			return "", "", fmt.Errorf("cluster ARN without a name")
		}
		return Cluster, rest, nil
	case service == "eks" && typ == "nodegroup":
		// nodegroup/<cluster>/<nodegroup>/<uuid>
		p := strings.Split(rest, "/")
		if len(p) < 2 {
			return "", "", fmt.Errorf("node group ARN without cluster and name")
		}
		return NodeGroup, p[0] + "/" + p[1], nil
	case service == "elasticloadbalancing" && typ == "loadbalancer":
		if !strings.HasPrefix(rest, "app/") && !strings.HasPrefix(rest, "net/") && !strings.HasPrefix(rest, "gwy/") {
			if rest == "" || strings.Contains(rest, "/") {
				return "", "", fmt.Errorf("load balancer ARN without a name")
			}
			return ClassicLB, rest, nil // loadbalancer/<name>
		}
		return LoadBalancer, arn, nil
	case service == "elasticloadbalancing" && typ == "targetgroup":
		return TargetGroup, arn, nil
	}
	return "", "", fmt.Errorf("type %s:%s is not handled yet", service, typ)
}

// HeartbeatKey is where the reaper records its last successful run in the
// state bucket. lab up --mode cloud refuses to start without a recent one.
const HeartbeatKey = "reaper/heartbeat.json"

// Heartbeat is the body of HeartbeatKey.
type Heartbeat struct {
	Time     time.Time `json:"time"`
	DryRun   bool      `json:"dryRun"`
	Deleted  int       `json:"deleted"`
	Managed  int       `json:"managed"`
	Waiting  int       `json:"waiting"`
	Teardown string    `json:"teardown,omitempty"`
	// ManualAction: the teardown gave up on an expired lab. lab up --mode
	// cloud refuses until it is cleared.
	ManualAction bool `json:"manualAction,omitempty"`
}
