package engine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/state"
)

// Action is what a plan step does to a stack.
type Action string

// Plan step actions.
const (
	Apply   Action = "apply"
	Destroy Action = "destroy"
	Keep    Action = "keep" // already applied; nothing to do
)

// Step is one stack and what will happen to it.
type Step struct {
	Stack  string `json:"stack"`
	Action Action `json:"action"`
	Reason string `json:"reason"`
}

// Plan is an ordered list of steps for one mode and profile.
type Plan struct {
	Mode       string `json:"mode"`
	Profile    string `json:"profile"`
	K8sVersion string `json:"k8sVersion"`
	Steps      []Step `json:"steps"`
	// Warnings are shown before the plan runs, e.g. stacks kept outside the profile.
	Warnings []string `json:"warnings,omitempty"`
}

// PlanOptions narrow PlanUp or change how it treats deployed stacks outside
// the profile.
type PlanOptions struct {
	// Stacks replaces the profile with these stacks and their dependencies.
	// Deployed stacks outside the selection are kept, without a warning.
	Stacks []string
	// Prune destroys deployed stacks outside the profile. Without it they are
	// kept and the plan warns. It cannot be combined with Stacks.
	Prune bool
}

// keepReason is the step reason for a deployed stack kept outside the profile.
const keepReason = "not in profile; run lab up --prune to remove"

// Changes counts the steps that are not Keep.
func (p Plan) Changes() int {
	n := 0
	for _, s := range p.Steps {
		if s.Action != Keep {
			n++
		}
	}
	return n
}

// PlanUp brings the lab to the profile in cfg: the profile's stacks are
// applied, dependencies first; applied ones are kept and failed ones retried.
// Deployed stacks outside the profile are destroyed first (dependents before
// dependencies) only with opts.Prune; otherwise they are kept, listed after the
// profile's stacks, and the plan carries a warning.
func PlanUp(g *Graph, cfg config.Config, st state.State, opts PlanOptions) (Plan, error) {
	if err := checkState(g, cfg, st); err != nil {
		return Plan{}, err
	}
	if opts.Prune && len(opts.Stacks) > 0 {
		return Plan{}, fmt.Errorf("prune applies to the whole profile; it cannot be combined with a stack selection")
	}
	want, err := ProfileStacks(cfg.Profile)
	if err != nil {
		return Plan{}, err
	}
	if len(opts.Stacks) > 0 {
		want = opts.Stacks
	}
	desired, err := g.Order(want...)
	if err != nil {
		return Plan{}, err
	}
	all, _ := g.Order(g.Names()...) // NewGraph proved this has no cycles
	var extra []string              // deployed but not wanted, dependency order
	for _, s := range all {
		if deployed(st, s) && !slices.Contains(desired, s) {
			extra = append(extra, s)
		}
	}
	p := newPlan(cfg)
	if opts.Prune {
		for _, s := range reversed(extra) {
			p.Steps = append(p.Steps, Step{s, Destroy, "not in profile " + cfg.Profile})
		}
	}
	for _, s := range desired {
		ss := st.Stacks[s]
		switch ss.Status {
		case state.Applied:
			p.Steps = append(p.Steps, Step{s, Keep, "applied"})
		case state.Failed:
			p.Steps = append(p.Steps, Step{s, Apply, "retry: " + ss.Error})
		default:
			p.Steps = append(p.Steps, Step{s, Apply, "not deployed"})
		}
	}
	if len(opts.Stacks) > 0 {
		for _, s := range extra {
			p.Steps = append(p.Steps, Step{s, Keep, "not selected"})
		}
	} else if !opts.Prune && len(extra) > 0 {
		// Desired stacks never depend on extras (desired is closed under
		// dependencies), so listing extras last keeps the order valid.
		for _, s := range extra {
			p.Steps = append(p.Steps, Step{s, Keep, keepReason})
		}
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d deployed %s not in profile %s, kept: %s. Run lab up --prune to remove.",
			len(extra), plural(len(extra), "stack is", "stacks are"), cfg.Profile, strings.Join(extra, ", ")))
	}
	return p, nil
}

// PlanDown destroys every deployed stack, dependents before dependencies.
func PlanDown(g *Graph, cfg config.Config, st state.State) (Plan, error) {
	if err := checkState(g, cfg, st); err != nil {
		return Plan{}, err
	}
	p := newPlan(cfg)
	all, _ := g.Order(g.Names()...)
	for _, s := range reversed(all) {
		if deployed(st, s) {
			p.Steps = append(p.Steps, Step{s, Destroy, "lab down"})
		}
	}
	return p, nil
}

func newPlan(cfg config.Config) Plan {
	return Plan{Mode: cfg.Mode, Profile: cfg.Profile, K8sVersion: cfg.K8sVersion}
}

// checkState refuses to plan across modes or over stacks the catalog no longer knows.
func checkState(g *Graph, cfg config.Config, st state.State) error {
	if st.Mode != "" && st.Mode != cfg.Mode && len(st.Stacks) > 0 {
		return fmt.Errorf("state holds a %s lab but lab.yaml says %s; run lab down in %s mode first", st.Mode, cfg.Mode, st.Mode)
	}
	for s := range st.Stacks {
		if !g.Has(s) {
			return fmt.Errorf("state has stack %q, which is not in the catalog", s)
		}
	}
	return nil
}

// deployed is true for stacks that may have resources: applied or failed part-way.
func deployed(st state.State, stack string) bool {
	s := st.Stacks[stack].Status
	return s == state.Applied || s == state.Failed
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func reversed(s []string) []string {
	r := slices.Clone(s)
	slices.Reverse(r)
	return r
}
