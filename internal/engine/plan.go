package engine

import (
	"fmt"
	"slices"

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
}

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

// PlanUp brings the lab to the profile in cfg. Stacks outside the profile are
// destroyed first, dependents before dependencies; then the profile's stacks
// are applied, dependencies first. Applied stacks are kept; failed ones retried.
func PlanUp(g *Graph, cfg config.Config, st state.State) (Plan, error) {
	if err := checkState(g, cfg, st); err != nil {
		return Plan{}, err
	}
	want, err := ProfileStacks(cfg.Profile)
	if err != nil {
		return Plan{}, err
	}
	desired, err := g.Order(want...)
	if err != nil {
		return Plan{}, err
	}
	p := newPlan(cfg)
	all, _ := g.Order(g.Names()...) // NewGraph proved this has no cycles
	for _, s := range reversed(all) {
		if deployed(st, s) && !slices.Contains(desired, s) {
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

func reversed(s []string) []string {
	r := slices.Clone(s)
	slices.Reverse(r)
	return r
}
