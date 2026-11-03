package local

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/chanupen/devops-lab/internal/labs"
)

// LabPrefix starts the name of every container lab makes: the main
// cluster's nodes (lab-control-plane, lab-worker...), scratch clusters'
// nodes (lab-scratch-<id>-control-plane) and the image cache (lab-zot).
const LabPrefix = "lab-"

// kindClusterLabel is the label kind puts on every node container.
const kindClusterLabel = "io.x-k8s.kind.cluster"

// Unit is a part of the lab made of containers: a kind cluster's nodes, or
// the image cache. After a reboot its containers are there but stopped.
type Unit struct {
	Kind       string   // "cluster", "image cache" or "container"
	Name       string   // the kind cluster's name, or the container's
	Containers []string // sorted
	Stopped    []string // the ones not running
	// Uptime is the first container's: a cluster's control-plane node.
	Uptime labs.Uptime
}

// Running reports whether every container of the unit runs.
func (u Unit) Running() bool { return len(u.Stopped) == 0 }

// Fix is the command that starts the stopped containers.
func (u Unit) Fix() string { return "podman start " + strings.Join(u.Stopped, " ") }

// nodeName matches a kind node container: <cluster>-control-plane, -worker, -worker2.
var nodeName = regexp.MustCompile(`^(.+)-(control-plane|worker\d*)$`)

// Units groups lab containers into the main cluster, then scratch
// clusters, then the image cache, then anything else.
func Units(cs []ContainerInfo) []Unit {
	var clusters, others []Unit
	var cache *Unit
	index := map[string]int{}
	for _, c := range cs {
		cluster := c.Labels[kindClusterLabel]
		if cluster == "" {
			if m := nodeName.FindStringSubmatch(c.Name); m != nil {
				cluster = m[1]
			}
		}
		var u *Unit
		switch {
		case cluster != "":
			i, ok := index[cluster]
			if !ok {
				i = len(clusters)
				index[cluster] = i
				clusters = append(clusters, Unit{Kind: "cluster", Name: cluster})
			}
			u = &clusters[i]
		case c.Name == ZotContainer:
			cache = &Unit{Kind: "image cache", Name: c.Name}
			u = cache
		default:
			others = append(others, Unit{Kind: "container", Name: c.Name})
			u = &others[len(others)-1]
		}
		if len(u.Containers) == 0 {
			u.Uptime = labs.Uptime{Exists: true, Running: c.Running, Started: c.StartedAt, Stopped: c.FinishedAt}
		}
		u.Containers = append(u.Containers, c.Name)
		if !c.Running {
			u.Stopped = append(u.Stopped, c.Name)
		}
	}
	// The main cluster first; Containers lists names sorted, so the
	// scratch clusters keep that order.
	out := make([]Unit, 0, len(clusters)+len(others)+1)
	if i, ok := index[ClusterName]; ok {
		out = append(out, clusters[i])
	}
	for _, u := range clusters {
		if u.Name != ClusterName {
			out = append(out, u)
		}
	}
	if cache != nil {
		out = append(out, *cache)
	}
	return append(out, others...)
}

// LabUnits lists the lab's containers as units.
func LabUnits(ctx context.Context, rt Runtime) ([]Unit, error) {
	cs, err := rt.Containers(ctx, LabPrefix)
	if err != nil {
		return nil, err
	}
	return Units(cs), nil
}

// HostUnits is LabUnits over the rootless Podman socket.
func HostUnits(ctx context.Context) ([]Unit, error) {
	sock, err := PodmanSocket()
	if err != nil {
		return nil, err
	}
	return LabUnits(ctx, NewPodman(sock))
}

// upOwns reports whether lab up starts the unit: the main cluster or the
// image cache.
func (u Unit) upOwns() bool {
	return u.Kind == "image cache" || (u.Kind == "cluster" && u.Name == ClusterName)
}

// StartStopped starts the stopped containers lab up owns, the main
// cluster's nodes and the image cache, and returns the units it started.
// After a reboot they are all there but stopped. Anything else stopped is
// left as it is and reported with the command that starts it: a scratch
// cluster belongs to lab labs start, and an unknown lab-* container is not
// lab up's to start.
func StartStopped(ctx context.Context, rt Runtime, log io.Writer) ([]Unit, error) {
	units, err := LabUnits(ctx, rt)
	if err != nil {
		return nil, fmt.Errorf("list lab containers: %w", err)
	}
	var started []Unit
	for _, u := range units {
		switch {
		case u.Running():
			continue
		case u.Kind == "cluster" && strings.HasPrefix(u.Name, labs.ScratchPrefix):
			logf(log, "scratch cluster %s is stopped; lab up leaves it (lab labs start %s starts it)",
				u.Name, strings.TrimPrefix(u.Name, labs.ScratchPrefix))
			continue
		case !u.upOwns():
			logf(log, "%s %s is stopped; lab up leaves it (%s)", u.Kind, u.Name, u.Fix())
			continue
		}
		logf(log, "%s %s was stopped; starting %s", u.Kind, u.Name, strings.Join(u.Stopped, ", "))
		for _, c := range u.Stopped {
			if err := rt.StartContainer(ctx, c); err != nil {
				return started, fmt.Errorf("start %s: %w", c, err)
			}
		}
		started = append(started, u)
	}
	return started, nil
}

// StartStopped starts the lab's stopped containers (StartStopped). When
// the main cluster's nodes were among them it waits until they are Ready,
// so what runs next finds the API server up.
func (p *Provider) StartStopped(ctx context.Context, log io.Writer) ([]Unit, error) {
	started, err := StartStopped(ctx, p.rt, log)
	if err != nil {
		return started, err
	}
	for _, u := range started {
		if u.Kind == "cluster" && u.Name == ClusterName {
			if err := p.nodesReady(ctx, p.opts.Kubeconfig, len(u.Containers), log); err != nil {
				return started, fmt.Errorf("cluster %s after starting its nodes: %w", ClusterName, err)
			}
		}
	}
	return started, nil
}
