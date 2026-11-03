package tui

import (
	"fmt"
	"strings"

	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/state"
)

// emptyHint is what Home says when nothing is deployed or the cluster is off.
const emptyHint = "press U to bring the lab up"

// homeModel is screen 1: a summary on the left, recent ops and app URLs on
// the right. It has no state of its own.
type homeModel struct{}

func (homeModel) view(d *data) (left, right string) {
	var l strings.Builder
	row := func(k, v string) { fmt.Fprintf(&l, "  %s %s\n", faint.Render(fmt.Sprintf("%-10s", k)), v) }
	l.WriteString(bold.Render(" Lab") + "\n\n")
	row("mode", fmt.Sprintf("%s (%s profile)", d.mode(), engine.ProfileName(d.cfg)))
	if d.cfgPath == "" {
		row("config", warn.Render("defaults")+faint.Render(" (no lab.yaml here or in ~/.lab)"))
	} else {
		row("config", faint.Render(d.cfgPath))
	}
	if len(d.st.Stacks) == 0 {
		l.WriteString("\n  Nothing is deployed.\n\n  " + warn.Render(emptyHint) + ".\n")
		return l.String(), homeRight(d)
	}
	rev := revision(d)
	gone := d.st.IsApplied(engine.RevisionStack) && d.revisionGone()
	if !gone && d.st.IsApplied(engine.RevisionStack) && d.st.Revision != engine.DefaultRevision {
		rev += faint.Render(" (not " + engine.DefaultRevision + ")")
	}
	row("revision", rev)
	if gone {
		row("", warn.Render(deletedHint))
	}
	c := d.live
	switch {
	case !c.known:
		row("cluster", statusProgressing.symbol()+" connecting")
	case !c.up:
		if u, _ := d.unit(UnitCluster, ""); d.clusterStopped() {
			row("cluster", statusOff.symbol()+" stopped "+faint.Render("$ "+u.Fix))
		} else {
			row("cluster", statusOff.symbol()+" off "+faint.Render("("+c.reason+")"))
		}
	default:
		s := statusHealthy
		if c.nodesReady < c.nodes {
			s = statusProgressing
		}
		row("nodes", fmt.Sprintf("%s %d/%d Ready", s.symbol(), c.nodesReady, c.nodes))
		if v, ok := versionRow(d); ok {
			row("version", v)
		}
		row("apps", appSummary(c))
	}
	row("stacks", stackSummary(d))
	if u, ok := d.unit(UnitCache, ""); ok {
		if u.Running {
			row("cache", statusHealthy.symbol()+" "+u.Name)
		} else {
			row("cache", statusOff.symbol()+" stopped "+faint.Render("$ "+u.Fix))
		}
	}
	if c.known && !c.up {
		l.WriteString("\n  " + warn.Render(emptyHint) + ".\n")
	}
	return l.String(), homeRight(d)
}

// appSummary counts Argo CD apps by status.
func appSummary(c cluster) string {
	if len(c.apps) == 0 {
		return faint.Render("none yet")
	}
	n := map[status]int{}
	for _, a := range c.apps {
		n[appStatus(a)]++
	}
	parts := []string{fmt.Sprintf("%s %d/%d ready", statusHealthy.symbol(), n[statusHealthy], len(c.apps))}
	if n[statusProgressing] > 0 {
		parts = append(parts, fmt.Sprintf("%s %d progressing", statusProgressing.symbol(), n[statusProgressing]))
	}
	if n[statusFailed] > 0 {
		parts = append(parts, fmt.Sprintf("%s %d failed", statusFailed.symbol(), n[statusFailed]))
	}
	return strings.Join(parts, "  ")
}

// stackSummary counts catalog stacks by status. Stacks that are not deployed
// are left out; with the cluster down every applied stack is off, so the
// summary says so instead of going blank.
func stackSummary(d *data) string {
	n := map[status]int{}
	for _, s := range d.order {
		st, _ := stackStatus(s, d.st, d.live)
		n[st]++
	}
	var parts []string
	if d.live.known && !d.live.up {
		parts = append(parts, statusOff.symbol()+" off — cluster not running")
	}
	for _, s := range []status{statusHealthy, statusProgressing, statusFailed} {
		if n[s] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d %s", s.symbol(), n[s], statusLooks[s].word))
		}
	}
	return strings.Join(parts, "  ")
}

func homeRight(d *data) string {
	var r strings.Builder
	r.WriteString(bold.Render(" Last operations") + "\n")
	ops := opsNewestFirst(d.st.Ops, 5)
	if len(ops) == 0 {
		r.WriteString(faint.Render("  none yet") + "\n")
	}
	for _, op := range ops {
		r.WriteString("  " + opLine(op) + "\n")
	}
	r.WriteString("\n" + bold.Render(" App URLs") + "\n")
	switch {
	case !d.live.up || d.appURL == nil:
		r.WriteString(faint.Render("  shown once the cluster is up") + "\n")
	case len(d.live.hosts) == 0:
		r.WriteString(faint.Render("  none yet (core and an app stack add them)") + "\n")
	default:
		for _, h := range d.live.hosts {
			r.WriteString("  " + d.appURL(h) + "\n")
		}
		if d.hint != "" {
			r.WriteString("  " + faint.Render(d.hint) + "\n")
		}
	}
	return r.String()
}

// opsNewestFirst returns up to n ops, newest first.
func opsNewestFirst(ops []state.Op, n int) []state.Op {
	var out []state.Op
	for i := len(ops) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, ops[i])
	}
	return out
}

// remakeHint makes the cluster again at the pinned version.
const remakeHint = "lab down, then lab up"

// versionRow is the main cluster's Kubernetes version from its node: ✕
// with the fix when it is not lab.yaml's pin or cannot be read.
func versionRow(d *data) (string, bool) {
	u, ok := d.unit(UnitCluster, "")
	if !ok || !u.Running {
		return "", false
	}
	pin := d.cfg.K8sVersion
	switch {
	case u.VersionErr != "":
		return statusFailed.symbol() + " unreadable " + faint.Render("("+u.VersionErr+") $ "+remakeHint), true
	case pin != "" && !strings.HasPrefix(u.Version, "v"+pin+"."):
		return fmt.Sprintf("%s %s, lab.yaml pins %s %s", statusFailed.symbol(), u.Version, pin, faint.Render("$ "+remakeHint)), true
	}
	return statusHealthy.symbol() + " " + u.Version, true
}
