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
	// Skip is a stack the profile asks for that the mode's provider cannot run
	// yet, or one that needs such a stack. Nothing runs for it.
	Skip Action = "skip"
)

// Step is one stack and what will happen to it.
type Step struct {
	Stack  string `json:"stack"`
	Action Action `json:"action"`
	Reason string `json:"reason"`
}

// Plan is an ordered list of steps for one mode and profile.
type Plan struct {
	Mode    string `json:"mode"`
	Profile string `json:"profile"`
	// Extra are lab.yaml's extraStacks that the profile does not already have.
	Extra      []string `json:"extra,omitempty"`
	K8sVersion string   `json:"k8sVersion"`
	// Revision is the Git revision the gitops stack syncs in this plan.
	Revision string `json:"revision,omitempty"`
	Steps    []Step `json:"steps"`
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
	// Revision overrides the Git revision for testing GitOps changes before
	// they merge. Empty means DefaultRevision. A deployed gitops stack on any
	// other revision is re-applied, so a plain lab up moves it back to main.
	Revision string
	// RefreshRepo re-applies a deployed gitops stack, which reloads Argo
	// CD's repository Secret from repo.sshKeyFile (a rotated deploy key).
	RefreshRepo bool
	// Supports reports whether the mode's provider can run a stack. Stacks it
	// cannot run, and stacks that need them, become Skip steps with one
	// warning instead of failing the run. Nil supports every stack.
	Supports func(stack string) bool
}

// DownOptions narrow PlanDown.
type DownOptions struct {
	// Stacks destroys only these stacks and the deployed stacks that need
	// them, dependents first. Empty destroys everything deployed.
	Stacks []string
}

// RevisionStack is the stack whose Git revision a plan tracks.
const RevisionStack = "gitops"

// DefaultRevision is what the lab syncs unless lab up --revision says otherwise.
const DefaultRevision = "main"

// keepReason is the step reason for a deployed stack kept outside the profile.
const keepReason = "not in profile; run lab up --prune to remove"

// Changes counts the steps that run: Apply and Destroy.
func (p Plan) Changes() int {
	n := 0
	for _, s := range p.Steps {
		if s.Action.Runs() {
			n++
		}
	}
	return n
}

// Runs reports whether a step with this action calls the provider.
func (a Action) Runs() bool { return a == Apply || a == Destroy }

// Count returns how many steps have action a.
func (p Plan) Count(a Action) int {
	n := 0
	for _, s := range p.Steps {
		if s.Action == a {
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
	want = append(want, cfg.ExtraStacks...)
	if len(opts.Stacks) > 0 {
		want = opts.Stacks
	}
	desired, err := g.Order(want...)
	if err != nil {
		return Plan{}, err
	}
	rev := opts.Revision
	if rev == "" {
		rev = DefaultRevision
	} else if !slices.Contains(desired, RevisionStack) {
		return Plan{}, fmt.Errorf("--revision applies to the %s stack, which this selection does not include", RevisionStack)
	}
	if opts.RefreshRepo && !slices.Contains(desired, RevisionStack) {
		return Plan{}, fmt.Errorf("--refresh-repo applies to the %s stack, which this selection does not include", RevisionStack)
	}
	all, _ := g.Order(g.Names()...) // NewGraph proved this has no cycles
	var extra []string              // deployed but not wanted, dependency order
	for _, s := range all {
		if deployed(st, s) && !slices.Contains(desired, s) {
			extra = append(extra, s)
		}
	}
	p := newPlan(cfg)
	profile := p.ProfileName()
	if slices.Contains(desired, RevisionStack) {
		p.Revision = rev
	}
	if opts.Prune {
		for _, s := range reversed(extra) {
			p.Steps = append(p.Steps, Step{s, Destroy, "not in profile " + profile})
		}
	}
	skipped := map[string]bool{}
	var skips []string
	for _, s := range desired {
		ss := st.Stacks[s]
		if why := skipReason(g, cfg.Mode, s, opts.Supports, skipped); why != "" && !deployed(st, s) {
			skipped[s] = true
			skips = append(skips, s)
			p.Steps = append(p.Steps, Step{s, Skip, why})
			continue
		}
		switch {
		case s == RevisionStack && ss.Status == state.Applied && st.Revision != rev:
			p.Steps = append(p.Steps, Step{s, Apply, fmt.Sprintf("revision %s -> %s", orUnrecorded(st.Revision), rev)})
		case s == RevisionStack && ss.Status == state.Applied && opts.RefreshRepo:
			p.Steps = append(p.Steps, Step{s, Apply, "reload the repository Secret from repo.sshKeyFile"})
		case ss.Status == state.Applied:
			p.Steps = append(p.Steps, Step{s, Keep, "applied"})
		case ss.Status == state.Failed:
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
			len(extra), plural(len(extra), "stack is", "stacks are"), profile, strings.Join(extra, ", ")))
	}
	if skipped[RevisionStack] {
		p.Revision = "" // nothing syncs a revision this run
	}
	if len(skips) > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d %s skipped, not supported by the %s provider yet: %s",
			len(skips), plural(len(skips), "stack", "stacks"), cfg.Mode, strings.Join(skips, ", ")))
	}
	if p.Revision != "" && p.Revision != DefaultRevision {
		p.Warnings = append(p.Warnings, fmt.Sprintf("gitops will track %s, not %s; a plain lab up moves it back", p.Revision, DefaultRevision))
	}
	return p, nil
}

// skipReason says why stack cannot run in this plan, or "" if it can: the
// provider does not support it, or it needs a stack that is skipped.
// Dependencies come first in plan order, so skipped already holds them.
func skipReason(g *Graph, mode, stack string, supports func(string) bool, skipped map[string]bool) string {
	if supports == nil {
		return ""
	}
	if !supports(stack) {
		return "not supported by the " + mode + " provider yet"
	}
	for _, d := range g.stacks[stack].DependsOn {
		if skipped[d] {
			return "needs " + d + ", which is skipped"
		}
	}
	return ""
}

func orUnrecorded(rev string) string {
	if rev == "" {
		return "unrecorded"
	}
	return rev
}

// PlanDown destroys deployed stacks, dependents before dependencies: all of
// them, or with opts.Stacks those stacks and every deployed stack that needs
// them.
func PlanDown(g *Graph, cfg config.Config, st state.State, opts DownOptions) (Plan, error) {
	if err := checkState(g, cfg, st); err != nil {
		return Plan{}, err
	}
	for _, s := range opts.Stacks {
		if !g.Has(s) {
			return Plan{}, fmt.Errorf("unknown stack %q", s)
		}
	}
	p := newPlan(cfg)
	all, _ := g.Order(g.Names()...)
	for _, s := range reversed(all) {
		if !deployed(st, s) {
			continue
		}
		switch {
		case len(opts.Stacks) == 0:
			p.Steps = append(p.Steps, Step{s, Destroy, "lab down"})
		case slices.Contains(opts.Stacks, s):
			p.Steps = append(p.Steps, Step{s, Destroy, "selected"})
		default:
			if by := g.neededBy(s, opts.Stacks); by != "" {
				p.Steps = append(p.Steps, Step{s, Destroy, "needs " + by})
			}
		}
	}
	return p, nil
}

func newPlan(cfg config.Config) Plan {
	p := Plan{Mode: cfg.Mode, Profile: cfg.Profile, K8sVersion: cfg.ClusterK8sVersion()}
	inProfile, _ := ProfileStacks(cfg.Profile)
	for _, s := range cfg.ExtraStacks {
		if !slices.Contains(inProfile, s) && !slices.Contains(p.Extra, s) {
			p.Extra = append(p.Extra, s)
		}
	}
	return p
}

// ProfileName is the profile with any extra stacks, e.g. "minimal + obs".
func (p Plan) ProfileName() string {
	if len(p.Extra) == 0 {
		return p.Profile
	}
	return p.Profile + " + " + strings.Join(p.Extra, ", ")
}

// ProfileName is cfg's profile with the extra stacks it does not already
// have, e.g. "minimal + obs".
func ProfileName(cfg config.Config) string { return newPlan(cfg).ProfileName() }

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
