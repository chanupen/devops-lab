// Package compat is the Kubernetes compatibility table both modes use: the
// version range each pinned component's upstream states for the release
// lab installs. The local lab's k8sVersion pin and the cloud lab's EKS
// version both have to fall inside the ranges of the rows for their mode.
package compat

import (
	"fmt"
	"slices"
	"strconv"
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
	Source       string   // where the range comes from
	Modes        []string // the lab modes that install it
	// Stack is the lab stack that installs it; "" for every lab of the mode.
	Stack string
	// OutsideCluster marks a component that runs beside the cluster (a
	// Podman container), so no Kubernetes version range applies to it.
	OutsideCluster bool
	// StatedMaxK8s is set when MaxK8s comes from upstream's tests, not a
	// stated support range: the highest version upstream states. Versions
	// above it and up to MaxK8s are "tested, not stated" (TestedNotStated).
	StatedMaxK8s string
}

// TestedNotStated reports whether v is inside the range only because
// upstream's tests cover it: above StatedMaxK8s, up to MaxK8s. lab allows
// it; lab doctor warns.
func (c Component) TestedNotStated(v string) bool {
	return c.StatedMaxK8s != "" && Less(c.StatedMaxK8s, v) && c.Supports(v)
}

// TestedNotStated lists the components, among those deployed, that support
// v only by upstream's tests. deployed reports whether a stack is up; rows
// with no stack always count.
func TestedNotStated(v string, comps []Component, deployed func(stack string) bool) []Component {
	var out []Component
	for _, c := range comps {
		if (c.Stack == "" || deployed(c.Stack)) && c.TestedNotStated(v) {
			out = append(out, c)
		}
	}
	return out
}

// Table is why each lab runs the Kubernetes version it does. Update the
// row whenever a pin moves; the tests next to each pin fail until it is.
//
// Checked on 2026-11-02:
//   - Karpenter 1.15: the v1.15.0 release notes say "bump max k8s version to
//     1.37" and the karpenter.sh matrix lists 1.37 → 1.15.x. (The matrix in
//     the v1.15.1 tag still stops at 1.36; that file was not updated on the
//     release branch.)
//   - AWS Load Balancer Controller: "v2.4.0+ requires Kubernetes 1.19+", no
//     upper bound. v3.6.2 is built against client-go v0.36.
//   - Argo CD 3.5: tested on 1.33 to 1.36. This row holds both labs at 1.36
//     until an Argo CD release lists 1.37.
//
// Checked on 2026-11-03 (EBS CSI driver on 2026-11-05):
//   - Kyverno 1.19 (chart 3.9.2): kyverno.io/docs/installation/releases lists
//     "Kubernetes Versions Supported: v1.33 - v1.35", but the release-1.19
//     conformance workflow (.github/workflows/tests-conformance.yaml) tests
//     v1.33.7, v1.34.3, v1.35.1 and v1.36.1. The row uses the tested range
//     and marks 1.36 as tested, not stated (StatedMaxK8s 1.35); lab doctor
//     warns while the policy stack runs on it. The only newer release is
//     v1.20.0-rc.1 (chart 3.10.0-rc.1), a release candidate.
//   - External Secrets Operator 2.13 (chart 2.13.0): external-secrets.io's
//     stability-support table lists Kubernetes 1.36 for 2.13 (one version;
//     2.14 lists 1.37). ESO supports only its latest release, and each
//     release lists one or two Kubernetes minors, so a Kubernetes bump
//     (k8sVersion, or a newer EKS choice) needs an ESO bump in the same PR.
//   - Moto 5.2.5 (motoserver/moto) runs as a Podman container beside the
//     cluster, so no Kubernetes range applies.
//   - AWS EBS CSI driver 1.67 (chart 2.67.1): the README's Compatibility
//     section says it works with every Kubernetes version the Kubernetes
//     project or EKS supports (extended support included); docs/install.md
//     requires 1.20 or later. No upper bound.
//   - Checked on 2026-11-06 (ai stack, local mode only for now):
//     Langfuse chart 2.2.3 (app 4.75.0) states no kubeVersion and no
//     supported range in its README. MinK8s 1.32 is derived, not stated:
//     its Postgres sub-chart sets a StatefulSet
//     persistentVolumeClaimRetentionPolicy, GA in 1.32. ClickHouse operator
//     0.0.8 (chart clickhouse-operator-helm 0.0.8, alpha API
//     clickhouse.com/v1alpha1): Chart.yaml kubeVersion ">= 1.28.0-0", and the
//     install docs say "Kubernetes cluster v1.28.0 or later". Ollama 0.40.13
//     is the host's Quadlet container, outside the cluster.
//   - kind v0.33.0 pre-builds node images for 1.34.11, 1.35.8, 1.36.4 and
//     1.37.0 (internal/provider/local NodeImages, digests from the release
//     notes).
var Table = []Component{
	{Name: "Karpenter", ChartVersion: "1.15.1", AppVersion: "1.15.1", MinK8s: "1.26", MaxK8s: "1.37",
		Source: "karpenter.sh/docs/upgrading/compatibility, v1.15.0 release notes", Modes: []string{"cloud"}, Stack: "cluster"},
	{Name: "AWS Load Balancer Controller", ChartVersion: "3.6.2", AppVersion: "v3.6.2", MinK8s: "1.19",
		Source: "aws-load-balancer-controller docs/deploy/installation.md", Modes: []string{"cloud"}, Stack: "cluster"},
	{Name: "AWS EBS CSI driver", ChartVersion: "2.67.1", AppVersion: "v1.67.1", MinK8s: "1.20",
		Source: "aws-ebs-csi-driver README (Compatibility) and docs/install.md", Modes: []string{"cloud"}, Stack: "cluster"},
	{Name: "Argo CD", ChartVersion: gitops.ArgoChartVersion, AppVersion: "v3.5.5", MinK8s: "1.33", MaxK8s: "1.36",
		Source: "argo-cd docs/operator-manual/tested-kubernetes-versions.md", Modes: []string{"local", "cloud"}, Stack: "gitops"},
	{Name: "Kyverno", ChartVersion: "3.9.2", AppVersion: "v1.19.2", MinK8s: "1.33", MaxK8s: "1.36",
		Source: "kyverno release-1.19 tests-conformance.yaml (tested v1.33–v1.36); kyverno.io releases page lists v1.33–v1.35",
		Modes:  []string{"local", "cloud"}, Stack: "policy", StatedMaxK8s: "1.35"},
	{Name: "External Secrets Operator", ChartVersion: "2.13.0", AppVersion: "v2.13.0", MinK8s: "1.36", MaxK8s: "1.36",
		Source: "external-secrets.io/latest/introduction/stability-support (2.13: Kubernetes 1.36)",
		Modes:  []string{"local", "cloud"}, Stack: "secrets"},
	{Name: "Moto", ChartVersion: "5.2.5", AppVersion: "5.2.5", OutsideCluster: true,
		Source: "runs as the lab-moto Podman container beside the cluster: no Kubernetes range",
		Modes:  []string{"local"}, Stack: "secrets"},
	{Name: "Langfuse", ChartVersion: "2.2.3", AppVersion: "4.75.0", MinK8s: "1.32",
		Source: "langfuse-k8s chart 2.2.3 states no range; 1.32 derived from its Postgres StatefulSet's persistentVolumeClaimRetentionPolicy (GA in 1.32)",
		Modes:  []string{"local"}, Stack: "ai"},
	{Name: "ClickHouse operator", ChartVersion: "0.0.8", AppVersion: "v0.0.8", MinK8s: "1.28",
		Source: "clickhouse-operator-helm 0.0.8 Chart.yaml kubeVersion >= 1.28.0-0; clickhouse.com/docs kubernetes-operator install (v1.28.0 or later)",
		Modes:  []string{"local"}, Stack: "ai"},
	{Name: "Ollama", ChartVersion: "0.40.13", AppVersion: "0.40.13", OutsideCluster: true,
		Source: "the host's Quadlet container (docker.io/ollama/ollama:0.40.13 by digest), reached through ollama.ai.svc: no Kubernetes range",
		Modes:  []string{"local"}, Stack: "ai"},
	{Name: "kind node images", ChartVersion: "v0.33.0", AppVersion: "v0.33.0", MinK8s: "1.34", MaxK8s: "1.37",
		Source: "kind v0.33.0 release notes, images pre-built for this release", Modes: []string{"local"}, Stack: "cluster"},
}

// For returns the rows for a lab mode.
func For(mode string) []Component {
	var out []Component
	for _, c := range Table {
		if slices.Contains(c.Modes, mode) {
			out = append(out, c)
		}
	}
	return out
}

// ForStacks is For(mode) without the rows of stacks that are not active: a
// row with a Stack limits the version only while that stack is deployed,
// in the profile or asked for. Rows without a Stack always count. active
// nil keeps every row (nothing is known about the lab yet).
func ForStacks(mode string, active []string) []Component {
	rows := For(mode)
	if active == nil {
		return rows
	}
	var out []Component
	for _, c := range rows {
		if c.Stack == "" || slices.Contains(active, c.Stack) {
			out = append(out, c)
		}
	}
	return out
}

// Supports reports whether the component's stated range includes v.
func (c Component) Supports(v string) bool {
	if c.OutsideCluster {
		return true
	}
	if c.MinK8s != "" && Less(v, c.MinK8s) {
		return false
	}
	return c.MaxK8s == "" || !Less(c.MaxK8s, v)
}

// Range is "1.33–1.36", or "1.19+" without an upper bound.
func (c Component) Range() string {
	if c.OutsideCluster {
		return "outside the cluster"
	}
	if c.MaxK8s == "" {
		return c.MinK8s + "+"
	}
	return c.MinK8s + "–" + c.MaxK8s
}

// Unsupported names the components that do not state support for v.
func Unsupported(v string, comps []Component) []string {
	var out []string
	for _, c := range comps {
		if !c.Supports(v) {
			out = append(out, fmt.Sprintf("%s %s (%s)", c.Name, c.AppVersion, c.Range()))
		}
	}
	return out
}

// Supported is the versions in vs that every component supports, oldest
// first.
func Supported(vs []string, comps []Component) []string {
	var ok []string
	for _, v := range Sorted(vs) {
		if len(Unsupported(v, comps)) == 0 {
			ok = append(ok, v)
		}
	}
	return ok
}

// Preferred is the newest version in vs that every component supports, or
// "" when there is none.
func Preferred(vs []string, comps []Component) string { return Newest(Supported(vs, comps)) }

// Newest returns the highest version in vs ("1.37" > "1.9").
func Newest(vs []string) string {
	best := ""
	for _, v := range vs {
		if best == "" || Less(best, v) {
			best = v
		}
	}
	return best
}

// Sorted returns vs oldest first.
func Sorted(vs []string) []string {
	out := slices.Clone(vs)
	slices.SortFunc(out, func(a, b string) int {
		switch {
		case Less(a, b):
			return -1
		case Less(b, a):
			return 1
		}
		return 0
	})
	return out
}

// Less compares dotted versions numerically: "1.9" < "1.37".
func Less(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}
