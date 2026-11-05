package aws

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/chanupen/devops-lab/internal/compat"
)

// Component is a row of the shared compatibility table.
type Component = compat.Component

// Compatibility is all of the table's cloud rows (internal/compat has the
// sources). Choosing a version uses only the active stacks' rows:
// compat.ForStacks("cloud", stacks).
var Compatibility = compat.For("cloud")

// Unsupported names the components that do not state support for v.
func Unsupported(v string, comps []Component) []string { return compat.Unsupported(v, comps) }

// PreferredVersion is the newest version in standard support that every
// component supports, or "" when there is none.
func PreferredVersion(standard []string, comps []Component) string {
	return compat.Preferred(standard, comps)
}

// VersionChoice is the cloud cluster's Kubernetes version and why.
type VersionChoice struct {
	Version string
	Reason  string
	// Warnings are worth showing but do not stop the lab: a pin outside a
	// component's stated range, or a running cluster older than preferred.
	Warnings []string
}

// ChooseVersion picks the cloud cluster's Kubernetes version. The rule is
// standard support only: extended support costs five times as much for the
// control plane. Within that:
//   - pinned (lab.yaml cloud.k8sVersion) wins if EKS has it in standard
//     support, with a warning if a component does not state support for it;
//   - else a running cluster keeps its version (current), so a new
//     preference never upgrades a live lab behind the user's back;
//   - else the newest version every component supports.
func ChooseVersion(pinned, current string, standard []string, comps []Component) (VersionChoice, error) {
	if len(standard) == 0 {
		return VersionChoice{}, fmt.Errorf("EKS reports no version in standard support")
	}
	list := strings.Join(sortedVersions(standard), ", ")
	preferred := PreferredVersion(standard, comps)
	inStandard := func(v string) bool { return slices.Contains(standard, v) }
	warn := func(v string) []string {
		var w []string
		if bad := Unsupported(v, comps); len(bad) > 0 {
			w = append(w, fmt.Sprintf("Kubernetes %s is outside the stated range of %s", v, strings.Join(bad, ", ")))
		}
		return w
	}
	switch {
	case pinned != "" && !inStandard(pinned):
		return VersionChoice{}, fmt.Errorf("cloud.k8sVersion %s is not in EKS standard support (standard: %s); "+
			"the cloud lab never runs on extended support: change or remove cloud.k8sVersion in lab.yaml", pinned, list)
	case pinned != "":
		return VersionChoice{Version: pinned, Reason: "pinned by cloud.k8sVersion, in standard support", Warnings: warn(pinned)}, nil
	case current != "" && !inStandard(current):
		return VersionChoice{}, fmt.Errorf("the running cluster is on Kubernetes %s, which has left EKS standard support (standard: %s); "+
			"lab down --mode cloud, then lab up --mode cloud starts a new one", current, list)
	case current != "":
		c := VersionChoice{Version: current, Reason: "the running cluster's version, in standard support", Warnings: warn(current)}
		if preferred != "" && versionLess(current, preferred) {
			c.Warnings = append(c.Warnings, fmt.Sprintf("a new cloud lab would get %s; this one keeps %s", preferred, current))
		}
		return c, nil
	case preferred == "":
		return VersionChoice{}, fmt.Errorf("no EKS version in standard support (%s) is supported by every pinned component: %s",
			list, describeComponents(comps))
	}
	return VersionChoice{Version: preferred, Reason: "the newest version in standard support that " + componentNames(comps) + " all support"}, nil
}

func sortedVersions(vs []string) []string { return compat.Sorted(vs) }

func componentNames(comps []Component) string {
	names := make([]string, len(comps))
	for i, c := range comps {
		names[i] = c.Name
	}
	return strings.Join(names, ", ")
}

func describeComponents(comps []Component) string {
	parts := make([]string, len(comps))
	for i, c := range comps {
		parts[i] = fmt.Sprintf("%s %s %s", c.Name, c.AppVersion, c.Range())
	}
	return strings.Join(parts, "; ")
}

// CheckNewStacks refuses to add stacks to a running cluster whose
// Kubernetes version their components do not support. A running cluster
// keeps its version (ChooseVersion), so a stack added later must fit it;
// a new cluster gets a version every active stack supports, so with no
// cluster there is nothing to check. stacks are the ones the plan applies
// that are not deployed yet.
func (p *Provider) CheckNewStacks(ctx context.Context, stacks []string) error {
	if len(stacks) == 0 {
		return nil
	}
	current, err := ClusterVersion(ctx, p.cfg, ClusterName)
	if err != nil || current == "" {
		return err
	}
	return CheckStacksOn(current, stacks)
}

// CheckStacksOn is CheckNewStacks for a cluster on Kubernetes v: an error
// naming every component of stacks whose stated range leaves v out.
func CheckStacksOn(v string, stacks []string) error {
	var bad []string
	for _, c := range compat.For("cloud") {
		if c.Stack != "" && slices.Contains(stacks, c.Stack) && !c.Supports(v) {
			bad = append(bad, fmt.Sprintf("%s (stack %s) %s supports Kubernetes %s", c.Name, c.Stack, c.AppVersion, c.Range()))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("the running cluster is on Kubernetes %s, which %s; a running cluster keeps its version, "+
		"so lab down --mode cloud, then lab up --mode cloud with the stack (a new cluster gets a version every active stack supports)",
		v, strings.Join(bad, "; "))
}
