package doctor

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/chanupen/devops-lab/internal/compat"
)

// scratchPrefix names the scratch labs' clusters (labs.ScratchPrefix).
const scratchPrefix = "lab-scratch-"

func stopped(fix, format string, a ...any) Result {
	return Result{Status: Stopped, Detail: fmt.Sprintf(format, a...), Fix: fix}
}

// unitCheck reports one lab unit: running, stopped (with the command that
// starts it) or not created yet.
func unitCheck(kind, name string) func(context.Context, System, Options) Result {
	return func(ctx context.Context, sys System, opts Options) Result {
		units, r, listed := listUnits(ctx, sys)
		if !listed {
			return r
		}
		for _, u := range units {
			if u.Kind != kind || u.Name != name {
				continue
			}
			if !u.Running {
				return stopped(u.Fix+" (or lab up)", "stopped")
			}
			return versionResult(u, opts.K8sVersion)
		}
		return Result{Status: Skip, Detail: "not created (lab up makes it)"}
	}
}

// checkScratchClusters reports the scratch labs' clusters; there are none
// while no lab is started.
func checkScratchClusters(ctx context.Context, sys System, _ Options) Result {
	units, r, listed := listUnits(ctx, sys)
	if !listed {
		return r
	}
	var running, down, fixes []string
	for _, u := range units {
		if u.Kind != "cluster" || !strings.HasPrefix(u.Name, scratchPrefix) {
			continue
		}
		if u.Running {
			running = append(running, u.Name)
		} else {
			down = append(down, u.Name)
			fixes = append(fixes, u.Fix)
		}
	}
	switch {
	case len(down) > 0:
		return stopped(strings.Join(fixes, "; ")+" (or lab labs start <id>)", "stopped: %s", strings.Join(down, ", "))
	case len(running) > 0:
		return ok("running: %s", strings.Join(running, ", "))
	}
	return Result{Status: Skip, Detail: "none (lab labs start makes one)"}
}

func listUnits(ctx context.Context, sys System) ([]Unit, Result, bool) {
	if sys.Units == nil {
		return nil, Result{Status: Skip, Detail: "not checked"}, false
	}
	units, err := sys.Units(ctx)
	if err != nil {
		return nil, Result{Status: Skip, Detail: "podman API unavailable: " + firstLine(err.Error())}, false
	}
	return units, Result{}, true
}

// remakeFix makes a kind cluster again at the pinned version.
const remakeFix = "lab down, then lab up (the image cache volume is kept)"

// versionResult adds a running cluster's version: ✕ when it is not the
// pin, ◐ when it cannot be read. Other units have no version.
func versionResult(u Unit, pin string) Result {
	r := runningResult(u)
	switch {
	case u.Kind != "cluster":
		return r
	case u.VersionErr != "":
		return warn(remakeFix, "%s; Kubernetes version unreadable: %s", r.Detail, u.VersionErr)
	case pin != "" && !strings.HasPrefix(u.Version, "v"+pin+"."):
		return fail(remakeFix, "%s, Kubernetes %s, but lab.yaml pins %s", r.Detail, u.Version, pin)
	}
	r.Detail += ", Kubernetes " + u.Version
	return r
}

func runningResult(u Unit) Result {
	if u.Nodes > 1 {
		return ok("running (%d nodes)", u.Nodes)
	}
	return ok("running")
}

// RegistryState is what the lab registry check found.
type RegistryState struct {
	Missing bool   // Zot is not there yet
	Pending string // a step still to run (the nodes' mirror file); a warning
	Err     error  // anything else: a failure
}

// checkLabRegistry reports whether labs can push images from the host and
// the cluster can pull them.
func checkLabRegistry(ctx context.Context, sys System, _ Options) Result {
	if sys.LabRegistry == nil {
		return Result{Status: Skip, Detail: "not checked"}
	}
	st := sys.LabRegistry(ctx)
	switch {
	case st.Missing:
		return Result{Status: Skip, Detail: "Zot is not there yet (lab up makes it)"}
	case st.Err != nil:
		return fail("lab up --stacks registry publishes Zot on 127.0.0.1:5000", "%s", firstLine(st.Err.Error()))
	case st.Pending != "":
		return warn("lab labs start (or lab up) writes it", "%s", st.Pending)
	}
	return ok("push 127.0.0.1:5000/lab from the host, pull lab-zot:5000/lab in the cluster")
}

// checkK8sSupport warns when a deployed component supports the pinned
// Kubernetes version only by upstream's tests, not a stated range: it
// works, and lab allows it, but no release promises it.
func checkK8sSupport(_ context.Context, _ System, opts Options) Result {
	if opts.K8sVersion == "" {
		return Result{Status: Skip, Detail: "no k8sVersion"}
	}
	deployed := func(s string) bool { return slices.Contains(opts.Stacks, s) }
	var notes []string
	for _, c := range compat.TestedNotStated(opts.K8sVersion, compat.For("local"), deployed) {
		notes = append(notes, fmt.Sprintf("%s %s is tested on %s by its CI, but states support only up to %s",
			c.Name, c.AppVersion, opts.K8sVersion, c.StatedMaxK8s))
	}
	if len(notes) > 0 {
		return warn("allowed: lab's table uses the tested range; move to a release that states "+opts.K8sVersion+" when there is one",
			"%s", strings.Join(notes, "; "))
	}
	return ok("every deployed component states support for Kubernetes %s", opts.K8sVersion)
}
