// Package reaper is the cloud lab's dead man's switch. Every 15 minutes it
// runs as a Lambda (cmd/reaper) and, for lab=true resources whose
// expires-at has passed:
//
//   - deletes what the in-cluster controllers created and OpenTofu does not
//     know about: Karpenter's instances, load balancers and target groups
//     from the Load Balancer Controller, their security groups, volumes;
//   - starts the teardown build (CodeBuild), which runs tofu destroy on the
//     cluster and registry state, sweeps again and emails a summary, for
//     everything OpenTofu manages (component=cluster or registry).
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
// nodes), then load balancers, the target groups and security groups they
// used, and last the volumes and addresses, free by then. Node groups and
// clusters are tofu-managed in the lab and normally go through the
// teardown build; they are here for strays.
const (
	NodeGroup     Kind = "eks-nodegroup"
	Instance      Kind = "ec2-instance"
	LoadBalancer  Kind = "load-balancer"
	TargetGroup   Kind = "target-group"
	SecurityGroup Kind = "security-group"
	Cluster       Kind = "eks-cluster"
	Volume        Kind = "ebs-volume"
	Address       Kind = "elastic-ip"
)

var order = map[Kind]int{NodeGroup: 0, Instance: 1, LoadBalancer: 2, TargetGroup: 3, SecurityGroup: 4, Cluster: 5, Volume: 6, Address: 7}

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

// Teardown starts the build that runs tofu destroy. It does nothing when a
// build is already running and says so.
type Teardown interface {
	Start(ctx context.Context) (status string, err error)
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
}

// Run finds expired lab resources, deletes the controller-created ones and
// starts the teardown for the rest, or only reports when opts.DryRun is
// set. A failed delete is reported and the run goes on.
func Run(ctx context.Context, l Lister, d Deleter, td Teardown, now time.Time, opts Options, logf func(string, ...any)) (Result, error) {
	res := Result{DryRun: opts.DryRun}
	found, err := l.List(ctx)
	if err != nil {
		return res, fmt.Errorf("list lab resources: %w", err)
	}
	var targets []Target
	for _, r := range found {
		t, why := classify(r, now)
		switch {
		case errors.Is(why, errKeep):
			res.Kept++
		case why != nil:
			res.Skipped = append(res.Skipped, r.ARN+": "+why.Error())
			logf("skip %s: %v", r.ARN, why)
		case t.Managed:
			res.Managed = append(res.Managed, string(t.Kind)+" "+t.ID)
		default:
			targets = append(targets, t)
		}
	}
	sort.SliceStable(targets, func(i, j int) bool { return order[targets[i].Kind] < order[targets[j].Kind] })
	for _, t := range targets {
		late := now.Sub(t.Expires).Round(time.Minute)
		if opts.DryRun {
			logf("dry run: would delete %s %s (expired %s ago)", t.Kind, t.ID, late)
			res.Deleted = append(res.Deleted, t)
			continue
		}
		err := d.Delete(ctx, t)
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
	if len(res.Managed) > 0 && !opts.SweepOnly {
		switch {
		case opts.DryRun:
			res.Teardown = "dry run: would start the teardown build"
		case td == nil:
			res.Errors = append(res.Errors, "teardown: no teardown build configured")
		default:
			status, err := td.Start(ctx)
			if err != nil {
				res.Errors = append(res.Errors, "teardown: "+err.Error())
			}
			res.Teardown = status
		}
		logf("%d expired tofu-managed resources; teardown: %s", len(res.Managed), orNone(res.Teardown))
	}
	return res, nil
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
			return "", "", fmt.Errorf("classic load balancers are not handled")
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
}
