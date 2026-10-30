package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/engine"
)

func newUpCmd(newProvider providerFactory) *cobra.Command {
	var (
		flags  labFlags
		stacks []string
		prune  bool
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Bring the lab up to the profile in lab.yaml",
		Long: "Plans against ~/.lab/state.json and applies stacks in dependency order. Stacks already\n" +
			"applied are kept; failed ones are retried. Deployed stacks outside the profile are kept\n" +
			"unless --prune is given.",
		Example: "  lab up\n  lab up --stacks cluster,registry",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, store, g, err := flags.load()
			if err != nil {
				return err
			}
			eng := engine.New(g, store, nil)
			p, err := eng.PlanUp(cfg, engine.PlanOptions{Stacks: stacks, Prune: prune})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			r := lipgloss.NewRenderer(out)
			if err := writePlan(out, r, p); err != nil {
				return err
			}
			if p.Changes() == 0 {
				_, err := fmt.Fprintln(out, "\nnothing to do")
				return err
			}
			if destroys := countAction(p, engine.Destroy); destroys > 0 {
				if err := confirm(cmd, yes, fmt.Sprintf("Destroy %d stacks outside the profile?", destroys)); err != nil {
					return err
				}
			}
			return run(cmd, newProvider, cfg, homeOf(store), g, store, p, false, upCommand(stacks, prune))
		},
	}
	flags.register(cmd)
	cmd.Flags().StringSliceVar(&stacks, "stacks", nil, "apply only these stacks and their dependencies")
	cmd.Flags().BoolVar(&prune, "prune", false, "destroy deployed stacks that are not in the profile")
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask before destroying stacks")
	cmd.MarkFlagsMutuallyExclusive("stacks", "prune")
	return cmd
}

// upCommand is the CLI command recorded in the ops log.
func upCommand(stacks []string, prune bool) string {
	c := "lab up"
	if len(stacks) > 0 {
		c += " --stacks " + strings.Join(stacks, ",")
	}
	if prune {
		c += " --prune"
	}
	return c
}

func countAction(p engine.Plan, a engine.Action) int {
	n := 0
	for _, s := range p.Steps {
		if s.Action == a {
			n++
		}
	}
	return n
}

// run builds the provider, checks it can handle every changed stack, then
// executes the plan and prints each step with its duration.
func run(cmd *cobra.Command, newProvider providerFactory, cfg config.Config, home string, g *engine.Graph,
	store engine.Store, p engine.Plan, purge bool, command string) error {
	out := cmd.OutOrStdout()
	prov, err := newProvider(cfg, home, purge, out)
	if err != nil {
		return err
	}
	if missing := unsupported(p, prov); len(missing) > 0 {
		return fmt.Errorf("the %s provider cannot run %s yet; use --stacks to pick supported stacks",
			p.Mode, strings.Join(missing, ", "))
	}
	r := lipgloss.NewRenderer(out)
	if _, err := fmt.Fprintf(out, "\n%s\n", r.NewStyle().Faint(true).Render("$ "+command)); err != nil {
		return err
	}
	start := time.Now()
	stepStart := map[string]time.Time{}
	err = engine.New(g, store, prov).Execute(cmd.Context(), p, command, func(e engine.Event) {
		writeEvent(out, r, e, stepStart)
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "\ndone in %s\n", time.Since(start).Round(100*time.Millisecond))
	return err
}

// Words for progress lines: present participle, past tense, verb.
var actionWords = map[engine.Action][3]string{
	engine.Apply:   {"applying", "applied", "apply"},
	engine.Destroy: {"destroying", "destroyed", "destroy"},
}

// writeEvent prints one progress line, using the state symbols from the TUI.
func writeEvent(w io.Writer, r *lipgloss.Renderer, e engine.Event, starts map[string]time.Time) {
	words := actionWords[e.Step.Action]
	ing, past, verb := words[0], words[1], words[2]
	switch e.Phase {
	case engine.Started:
		starts[e.Step.Stack] = time.Now()
		_, _ = fmt.Fprintf(w, "%s %s: %s\n", r.NewStyle().Foreground(lipgloss.Color("3")).Render("◐"), e.Step.Stack, ing)
	case engine.Finished:
		d := time.Since(starts[e.Step.Stack]).Round(100 * time.Millisecond)
		_, _ = fmt.Fprintf(w, "%s %s: %s in %s\n", r.NewStyle().Foreground(lipgloss.Color("2")).Render("●"), e.Step.Stack, past, d)
	case engine.Failed:
		d := time.Since(starts[e.Step.Stack]).Round(100 * time.Millisecond)
		_, _ = fmt.Fprintf(w, "%s %s: %s failed after %s\n", r.NewStyle().Foreground(lipgloss.Color("1")).Render("✕"), e.Step.Stack, verb, d)
	}
}
