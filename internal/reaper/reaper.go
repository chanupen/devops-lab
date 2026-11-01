// Package reaper deletes lab resources whose expires-at tag has passed. It
// runs as a Lambda on a 15-minute schedule (cmd/reaper). Only resources
// tagged lab=true with a valid, past expires-at are touched; resources with
// no expires-at (the account baseline) are always kept.
package reaper

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Tag keys.
const (
	LabTag     = "lab"
	ExpiresTag = "expires-at"
)

// Kind is a resource type the reaper knows how to delete.
type Kind string

// Kinds, in the order the reaper deletes them:
//
//  1. EKS node groups, which takes down the controllers running on them
//     (Karpenter, the Load Balancer Controller), so nothing relaunches
//     instances or recreates load balancers behind the reaper;
//  2. instances, including the spot nodes Karpenter launched;
//  3. load balancers the Load Balancer Controller created;
//  4. EKS clusters, once none of their node groups is left;
//  5. volumes and addresses, free by then.
const (
	NodeGroup    Kind = "eks-nodegroup"
	Instance     Kind = "ec2-instance"
	LoadBalancer Kind = "load-balancer"
	Cluster      Kind = "eks-cluster"
	Volume       Kind = "ebs-volume"
	Address      Kind = "elastic-ip"
)

var order = map[Kind]int{NodeGroup: 0, Instance: 1, LoadBalancer: 2, Cluster: 3, Volume: 4, Address: 5}

// ErrNotYet is returned by a Deleter when a resource cannot go yet, such as
// a cluster whose node groups are still deleting. The run reports it as
// waiting, not failed; the next run (15 minutes later) tries again.
var ErrNotYet = errors.New("not yet")

// NodeGroupTag is set by EKS on the instances of a managed node group.
// Deleting the node group removes them, so the reaper leaves them to it.
const NodeGroupTag = "eks:nodegroup-name"

// Resource is one tagged resource as the tagging API reports it.
type Resource struct {
	ARN  string
	Tags map[string]string
}

// Target is a resource the reaper will delete.
type Target struct {
	Kind    Kind
	ARN     string
	ID      string // instance, volume or allocation ID; "cluster/nodegroup" for node groups; the name for clusters; the ARN for load balancers
	Expires time.Time
}

// Lister finds resources tagged lab=true.
type Lister interface {
	List(ctx context.Context) ([]Resource, error)
}

// Deleter deletes one target.
type Deleter interface {
	Delete(ctx context.Context, t Target) error
}

// Result is what one run did.
type Result struct {
	DryRun  bool
	Deleted []Target // or, in dry run, would delete
	Kept    int      // tagged lab=true but not expired, or no expires-at
	Skipped []string // could not be handled: unknown type or bad expires-at
	Waiting []string // expired, but must wait for something else to go first
	Errors  []string
}

// Run finds expired lab resources and deletes them, or only reports them
// when dryRun is set. A failed delete is reported and the run goes on.
func Run(ctx context.Context, l Lister, d Deleter, now time.Time, dryRun bool, logf func(string, ...any)) (Result, error) {
	res := Result{DryRun: dryRun}
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
		default:
			targets = append(targets, t)
		}
	}
	sort.SliceStable(targets, func(i, j int) bool { return order[targets[i].Kind] < order[targets[j].Kind] })
	for _, t := range targets {
		late := now.Sub(t.Expires).Round(time.Minute)
		if dryRun {
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
	return res, nil
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
	kind, id, err := parseARN(r.ARN)
	if err != nil {
		return Target{}, err
	}
	if kind == Instance && r.Tags[NodeGroupTag] != "" {
		return Target{}, errKeep // its node group's deletion removes it
	}
	return Target{Kind: kind, ARN: r.ARN, ID: id, Expires: exp}, nil
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
	}
	return "", "", fmt.Errorf("type %s:%s is not handled yet", service, typ)
}
