package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/chanupen/devops-lab/internal/gitops"
	"github.com/chanupen/devops-lab/internal/kube"
	"github.com/chanupen/devops-lab/internal/state"
)

// status is how a stack, app or op reads on screen: a symbol and a colour
// together, so it reads on any terminal theme.
type status int

const (
	statusOff status = iota
	statusProgressing
	statusHealthy
	statusFailed
)

var statusLooks = map[status]struct {
	symbol string
	color  lipgloss.Color
	word   string
}{
	statusOff:         {"○", "8", "off"},
	statusProgressing: {"◐", "3", "progressing"},
	statusHealthy:     {"●", "2", "healthy"},
	statusFailed:      {"✕", "1", "failed"},
}

func (s status) symbol() string {
	return lipgloss.NewStyle().Foreground(statusLooks[s].color).Render(statusLooks[s].symbol)
}

func (s status) word() string {
	return lipgloss.NewStyle().Foreground(statusLooks[s].color).Render(statusLooks[s].word)
}

var (
	faint  = lipgloss.NewStyle().Faint(true)
	bold   = lipgloss.NewStyle().Bold(true)
	warn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	danger = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	code   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

// cluster is the live state the informers report. The zero value means no
// report yet: the screens show what state.json says.
type cluster struct {
	known      bool   // a connect attempt or informer sync has finished
	up         bool   // the API server answers
	reason     string // why it is down
	nodes      int
	nodesReady int
	apps       []kube.App // sorted by name
	hosts      []string   // HTTPRoute hostnames on the lab Gateway
}

// stackApps returns the Argo CD apps that belong to stack.
func (c cluster) stackApps(stack string) []kube.App {
	var out []kube.App
	for _, a := range c.apps {
		if a.Labels[gitops.StackLabel] == stack {
			out = append(out, a)
		}
	}
	return out
}

// appStatus maps an Argo CD app to a status.
func appStatus(a kube.App) status {
	switch {
	case a.Ready():
		return statusHealthy
	case a.Health == "Degraded" || len(a.Problems) > 0:
		return statusFailed
	default:
		return statusProgressing
	}
}

// infraStacks are applied by providers, not Argo CD, so they have no apps.
var infraStacks = map[string]bool{"registry": true, "cluster": true}

// stackStatus combines the recorded result for a stack with what the cluster
// reports. Before the first report it trusts state.json; with the cluster
// down every applied stack is off, which is a hint, not an error.
func stackStatus(name string, st state.State, c cluster) (status, string) {
	ss, ok := st.Stacks[name]
	switch {
	case !ok:
		return statusOff, "not deployed"
	case ss.Status == state.Failed:
		return statusFailed, "failed"
	case ss.Status != state.Applied:
		return statusProgressing, ss.Status
	case !c.known:
		return statusHealthy, "applied"
	case !c.up:
		return statusOff, "cluster off"
	case name == "registry":
		return statusHealthy, "applied"
	case name == "cluster":
		if c.nodes > 0 && c.nodesReady == c.nodes {
			return statusHealthy, fmt.Sprintf("%d/%d nodes Ready", c.nodesReady, c.nodes)
		}
		return statusProgressing, fmt.Sprintf("%d/%d nodes Ready", c.nodesReady, c.nodes)
	}
	apps := c.stackApps(name)
	if len(apps) == 0 {
		return statusProgressing, "waiting for Argo CD apps"
	}
	ready, failed := 0, 0
	for _, a := range apps {
		switch appStatus(a) {
		case statusHealthy:
			ready++
		case statusFailed:
			failed++
		}
	}
	text := fmt.Sprintf("%d/%d apps ready", ready, len(apps))
	switch {
	case failed > 0:
		return statusFailed, text
	case ready == len(apps):
		return statusHealthy, text
	default:
		return statusProgressing, text
	}
}

// opStatus maps an ops log entry to a status.
func opStatus(op state.Op) status {
	if op.Status == "ok" {
		return statusHealthy
	}
	return statusFailed
}

// firstLine trims s to its first line.
func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
