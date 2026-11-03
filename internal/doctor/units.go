package doctor

import (
	"context"
	"fmt"
	"strings"
)

// scratchPrefix names the scratch labs' clusters (labs.ScratchPrefix).
const scratchPrefix = "lab-scratch-"

func stopped(fix, format string, a ...any) Result {
	return Result{Status: Stopped, Detail: fmt.Sprintf(format, a...), Fix: fix}
}

// unitCheck reports one lab unit: running, stopped (with the command that
// starts it) or not created yet.
func unitCheck(kind, name string) func(context.Context, System, Options) Result {
	return func(ctx context.Context, sys System, _ Options) Result {
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
			return runningResult(u)
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

func runningResult(u Unit) Result {
	if u.Nodes > 1 {
		return ok("running (%d nodes)", u.Nodes)
	}
	return ok("running")
}
