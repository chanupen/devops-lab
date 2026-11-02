package aws

import (
	"fmt"
	"slices"
	"strings"

	"github.com/chanupen/devops-lab/internal/gitops"
)

// Component is something lab pins that must support the cluster's
// Kubernetes version. MinK8s and MaxK8s are the range its upstream states
// for the pinned release; an empty MaxK8s means upstream states no upper
// bound.
type Component struct {
	Name         string
	ChartVersion string // the pin in code; a test keeps the two in step
	AppVersion   string
	MinK8s       string
	MaxK8s       string
	Source       string // where the range comes from
}

// Compatibility is why the cloud lab runs the Kubernetes version it does.
// Update the row whenever a pin moves; TestCompatibilityMatchesPins fails
// until it is.
//
// Checked on 2026-11-02:
//   - Karpenter 1.15: the v1.15.0 release notes say "bump max k8s version to
//     1.37" and the karpenter.sh matrix lists 1.37 → 1.15.x. (The matrix in
//     the v1.15.1 tag still stops at 1.36; that file was not updated on the
//     release branch.)
//   - AWS Load Balancer Controller: "v2.4.0+ requires Kubernetes 1.19+", no
//     upper bound. v3.6.2 is built against client-go v0.36.
//   - Argo CD 3.5: tested on 1.33 to 1.36. This row holds the cloud lab
//     at 1.36 until an Argo CD release lists 1.37.
var Compatibility = []Component{
	{Name: "Karpenter", ChartVersion: KarpenterChart.Version, AppVersion: "1.15.1", MinK8s: "1.26", MaxK8s: "1.37",
		Source: "karpenter.sh/docs/upgrading/compatibility, v1.15.0 release notes"},
	{Name: "AWS Load Balancer Controller", ChartVersion: LBCChart.Version, AppVersion: "v3.6.2", MinK8s: "1.19",
		Source: "aws-load-balancer-controller docs/deploy/installation.md"},
	{Name: "Argo CD", ChartVersion: gitops.ArgoChartVersion, AppVersion: "v3.5.5", MinK8s: "1.33", MaxK8s: "1.36",
		Source: "argo-cd docs/operator-manual/tested-kubernetes-versions.md"},
}

// Supports reports whether the component's stated range includes v.
func (c Component) Supports(v string) bool {
	if c.MinK8s != "" && versionLess(v, c.MinK8s) {
		return false
	}
	return c.MaxK8s == "" || !versionLess(c.MaxK8s, v)
}

// Unsupported names the components that do not state support for v.
func Unsupported(v string, comps []Component) []string {
	var out []string
	for _, c := range comps {
		if !c.Supports(v) {
			out = append(out, fmt.Sprintf("%s %s (%s)", c.Name, c.AppVersion, rangeOf(c)))
		}
	}
	return out
}

func rangeOf(c Component) string {
	if c.MaxK8s == "" {
		return c.MinK8s + "+"
	}
	return c.MinK8s + "–" + c.MaxK8s
}

// PreferredVersion is the newest version in standard support that every
// component supports, or "" when there is none.
func PreferredVersion(standard []string, comps []Component) string {
	var ok []string
	for _, v := range standard {
		if len(Unsupported(v, comps)) == 0 {
			ok = append(ok, v)
		}
	}
	return NewestVersion(ok)
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

func sortedVersions(vs []string) []string {
	out := slices.Clone(vs)
	slices.SortFunc(out, func(a, b string) int {
		switch {
		case versionLess(a, b):
			return -1
		case versionLess(b, a):
			return 1
		}
		return 0
	})
	return out
}

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
		parts[i] = fmt.Sprintf("%s %s %s", c.Name, c.AppVersion, rangeOf(c))
	}
	return strings.Join(parts, "; ")
}
