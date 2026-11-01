package tui

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chanupen/devops-lab/internal/engine"
)

// k9sInstallHint is shown when K finds no k9s on PATH.
const k9sInstallHint = "k9s is not installed: get it from https://github.com/derailed/k9s/releases, then press K again"

// defaultContext is the lab's kube context in local mode.
const defaultContext = "kind-lab"

// k9sDoneMsg reports that k9s exited and the TUI is back.
type k9sDoneMsg struct{ err error }

// k9s suspends the TUI and runs k9s against the lab cluster. Bubble Tea
// restores the screen, and the same screen, when it exits.
func (m Model) k9s() (Model, tea.Cmd) {
	path, err := m.opts.LookPath("k9s")
	if err != nil {
		m.flash = statusOff.symbol() + " " + k9sInstallHint
		return m, nil
	}
	if _, err := os.Stat(m.opts.Kubeconfig); err != nil {
		m.flash = statusOff.symbol() + " K needs the lab cluster: " + emptyHint
		return m, nil
	}
	ctx := m.d.ctx
	if ctx == "" {
		ctx = defaultContext
	}
	args := []string{"--context", ctx, "--kubeconfig", m.opts.Kubeconfig}
	m.flash = "$ k9s " + strings.Join(args, " ")
	return m, tea.ExecProcess(exec.Command(path, args...), func(err error) tea.Msg { return k9sDoneMsg{err: err} })
}

// branchMsg reports whether the recorded GitOps branch still exists.
type branchMsg struct {
	rev    string
	exists bool
	err    error
}

// checkBranch asks, off the key loop, whether a non-main revision still
// exists on the remote. Nil when there is nothing to check.
func (m Model) checkBranch() tea.Cmd {
	rev := m.d.st.Revision
	if m.opts.BranchExists == nil || rev == "" || rev == engine.DefaultRevision ||
		!m.d.st.IsApplied(engine.RevisionStack) || m.d.branchChecked[rev] {
		return nil
	}
	m.d.branchChecked[rev] = true // a map, so the copy in Update sees it
	exists := m.opts.BranchExists
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ok, err := exists(ctx, rev)
		return branchMsg{rev: rev, exists: ok, err: err}
	}
}
