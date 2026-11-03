package main

import (
	"context"
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
		flags    labFlags
		stacks   []string
		prune    bool
		yes      bool
		timeout  time.Duration
		revision string
		ttl      time.Duration
	)
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Bring the lab up to the profile in lab.yaml",
		Long: "In local mode it first starts the main cluster's nodes and the image cache when they are\n" +
			"stopped, as after a reboot; other stopped lab-* containers are reported, not started.\n" +
			"Then it plans against ~/.lab/state.json and applies stacks in dependency order. Stacks\n" +
			"already applied are kept; failed ones are retried. Deployed stacks outside the profile\n" +
			"are kept unless --prune is given.",
		Example: "  lab up\n  lab up --stacks cluster,registry\n  lab up --stacks obs --revision feat/obs\n  lab up --mode cloud --ttl 20m",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, store, g, err := flags.load()
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("ttl") {
				if cfg.Mode != "cloud" {
					return fmt.Errorf("--ttl applies to cloud mode only")
				}
				if err := config.CheckTTL(ttl); err != nil {
					return fmt.Errorf("--ttl %w", err)
				}
				cfg.Cloud.TTL = config.Duration(ttl)
			}
			command := upCommand(stacks, prune, revision)
			eng := engine.New(g, store, nil)
			opts := engine.PlanOptions{Stacks: stacks, Prune: prune, Revision: revision}
			// Plan once without the provider so a bad request fails before
			// one is built, then again with what it supports.
			if _, err := eng.PlanUp(cfg, opts); err != nil {
				return err
			}
			prov, err := newProvider(cfg, homeOf(store), flags.cloudRunOptions(cmd, runOptions{Revision: revision, Timeout: timeout}))
			if err != nil {
				return err
			}
			// A pin outside the compatibility table is refused before
			// anything else happens, even before stopped containers start.
			if g, ok := prov.(versionGate); ok {
				if err := g.CheckK8sVersion(cmd.Context()); err != nil {
					return err
				}
			}
			// After a reboot the lab's containers are there but stopped:
			// start the main cluster and the cache before anything else
			// looks at them.
			if s, ok := prov.(stoppedStarter); ok {
				if _, err := s.StartStopped(cmd.Context(), cmd.OutOrStdout()); err != nil {
					return fmt.Errorf("starting the lab's stopped containers: %w", err)
				}
			}
			opts.Supports = prov.Supports
			p, err := eng.PlanUp(cfg, opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if err := writePlan(out, lipgloss.NewRenderer(out), p); err != nil {
				return err
			}
			if p.Changes() == 0 {
				if _, err := fmt.Fprintln(out, "\nnothing to do"); err != nil {
					return err
				}
			} else {
				if err := confirmCloudApply(cmd, prov, p, time.Duration(cfg.Cloud.TTL), yes); err != nil {
					return err
				}
				if destroys := p.Count(engine.Destroy); destroys > 0 {
					if err := confirm(cmd, yes, fmt.Sprintf("Destroy %d stacks outside the profile?", destroys)); err != nil {
						return err
					}
				}
				if err := run(cmd, prov, g, store, p, command); err != nil {
					return err
				}
			}
			return printApps(cmd, prov, store)
		},
	}
	flags.register(cmd)
	flags.registerMode(cmd)
	cmd.Flags().StringSliceVar(&stacks, "stacks", nil, "apply only these stacks and their dependencies")
	cmd.Flags().BoolVar(&prune, "prune", false, "destroy deployed stacks that are not in the profile")
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask before destroying stacks or, in cloud mode, applying to AWS")
	cmd.Flags().StringVar(&revision, "revision", "",
		"sync the gitops stack from this Git branch to test changes before they merge (default main)")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "cloud mode: lifetime of a new lab (default cloud.ttl in lab.yaml, at most 8h); the reaper deletes it after that")
	registerTimeout(cmd, &timeout)
	cmd.MarkFlagsMutuallyExclusive("stacks", "prune")
	return cmd
}

// upCommand is the CLI command recorded in the ops log.
func upCommand(stacks []string, prune bool, revision string) string {
	c := "lab up"
	if len(stacks) > 0 {
		c += " --stacks " + strings.Join(stacks, ",")
	}
	if prune {
		c += " --prune"
	}
	if revision != "" {
		c += " --revision " + revision
	}
	return c
}

// run checks the provider can handle every changed stack, then executes the
// plan and prints each step with its duration.
func run(cmd *cobra.Command, prov provider, g *engine.Graph, store engine.Store, p engine.Plan, command string) error {
	out := cmd.OutOrStdout()
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
	err := engine.New(g, store, prov).Execute(cmd.Context(), p, command, func(e engine.Event) {
		writeEvent(out, r, e, stepStart)
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "\ndone in %s\n", time.Since(start).Round(100*time.Millisecond))
	return err
}

// gatewayStack is the stack that creates the shared Gateway apps route through.
const gatewayStack = "core"

// printApps lists app URLs and how to reach them once the Gateway exists.
// Listing is best effort: a failure is a warning, not a failed lab up.
func printApps(cmd *cobra.Command, prov provider, store engine.Store) error {
	st, err := store.Load()
	if err != nil || !st.IsApplied(gatewayStack) {
		return nil
	}
	a, ok := prov.(appAccess)
	if !ok {
		return nil
	}
	out := cmd.OutOrStdout()
	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()
	urls, err := a.AppURLs(ctx)
	if err != nil {
		_, err = fmt.Fprintf(out, "\nwarning: could not list app URLs: %v\n", err)
		return err
	}
	var b strings.Builder
	b.WriteString("\nApps:\n")
	if len(urls) == 0 {
		b.WriteString("  none yet (no HTTPRoutes on the lab Gateway)\n")
	}
	for _, u := range urls {
		b.WriteString("  " + u + "\n")
	}
	b.WriteString(a.AccessHint() + "\n")
	_, err = io.WriteString(out, b.String())
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
		_, _ = fmt.Fprintf(w, "%s %s: %s %s\n", r.NewStyle().Foreground(lipgloss.Color("3")).Render("◐"), e.Step.Stack, ing,
			r.NewStyle().Faint(true).Render("(log: "+e.Log+")"))
	case engine.Finished:
		d := time.Since(starts[e.Step.Stack]).Round(100 * time.Millisecond)
		_, _ = fmt.Fprintf(w, "%s %s: %s in %s\n", r.NewStyle().Foreground(lipgloss.Color("2")).Render("●"), e.Step.Stack, past, d)
	case engine.Failed:
		d := time.Since(starts[e.Step.Stack]).Round(100 * time.Millisecond)
		_, _ = fmt.Fprintf(w, "%s %s: %s failed after %s\n", r.NewStyle().Foreground(lipgloss.Color("1")).Render("✕"), e.Step.Stack, verb, d)
	}
}
