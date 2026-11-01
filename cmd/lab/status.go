package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/state"
)

func newStatusCmd(newProvider providerFactory) *cobra.Command {
	var flags labFlags
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show stacks, the GitOps revision and app URLs",
		Long:  "Reads ~/.lab/state.json and lists every stack in dependency order, the Git revision\nthe gitops stack syncs, and, once core is up, the app URLs. Changes nothing.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, store, g, err := flags.load()
			if err != nil {
				return err
			}
			st, err := store.Load()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if err := writeStatus(out, lipgloss.NewRenderer(out), cfg, st, g); err != nil {
				return err
			}
			if !st.IsApplied(gatewayStack) {
				return nil
			}
			prov, err := newProvider(cfg, homeOf(store), runOptions{Revision: st.Revision})
			if err != nil {
				_, err = fmt.Fprintf(out, "\nwarning: could not list app URLs: %v\n", err)
				return err
			}
			return printApps(cmd, prov, store)
		},
	}
	flags.register(cmd)
	return cmd
}

func writeStatus(w io.Writer, r *lipgloss.Renderer, cfg config.Config, st state.State, g *engine.Graph) error {
	var b strings.Builder
	mode := st.Mode
	if mode == "" {
		mode = cfg.Mode
	}
	fmt.Fprintf(&b, "%s mode, %s profile, Kubernetes %s\n", mode, cfg.Profile, cfg.K8sVersion)
	warn := r.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	switch {
	case !st.IsApplied(engine.RevisionStack):
		b.WriteString("gitops revision: - (gitops not deployed)\n")
	case st.Revision == engine.DefaultRevision:
		fmt.Fprintf(&b, "gitops revision: %s\n", st.Revision)
	case st.Revision == "":
		b.WriteString("gitops revision: " + warn.Render("unrecorded") + " (lab up records it and moves gitops to main)\n")
	default:
		fmt.Fprintf(&b, "gitops revision: %s (not %s; a plain lab up moves it back)\n",
			warn.Render(st.Revision), engine.DefaultRevision)
	}
	b.WriteString("\n")
	all, err := g.Order(g.Names()...)
	if err != nil {
		return err
	}
	width := 0
	for _, s := range all {
		width = max(width, len(s))
	}
	for _, s := range all {
		ss, ok := st.Stacks[s]
		sym, col, text := "○", lipgloss.Color("8"), "not deployed"
		switch {
		case ok && ss.Status == state.Applied:
			sym, col, text = "●", lipgloss.Color("2"), "applied"
		case ok && ss.Status == state.Failed:
			sym, col, text = "✕", lipgloss.Color("1"), "failed: "+ss.Error
		}
		when := ""
		if ok && !ss.UpdatedAt.IsZero() {
			when = "  " + r.NewStyle().Faint(true).Render(ss.UpdatedAt.Local().Format("2006-01-02 15:04"))
		}
		fmt.Fprintf(&b, "  %s %-*s  %s%s\n", r.NewStyle().Foreground(col).Render(sym), width, s, text, when)
	}
	_, err = io.WriteString(w, b.String())
	return err
}
