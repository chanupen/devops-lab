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
	"github.com/chanupen/devops-lab/internal/state"
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
		refresh  bool
	)
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Bring the lab up to the profile in lab.yaml",
		Long: "In local mode it first starts the main cluster's nodes and the image cache when they are\n" +
			"stopped, as after a reboot; other stopped lab-* containers are reported, not started.\n" +
			"Then it plans against ~/.lab/state.json and applies stacks in dependency order. Stacks\n" +
			"already applied are kept; failed ones are retried. Deployed stacks outside the profile\n" +
			"are kept unless --prune is given.",
		Example: "  lab up\n  lab up --stacks cluster,registry\n  lab up --stacks obs --revision feat/obs\n" +
			"  lab up --stacks gitops --refresh-repo   # after rotating the deploy key\n  lab up --mode cloud --ttl 20m",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, store, g, err := flags.load()
			if err != nil {
				return err
			}
			if err := restoreDrill(cmd, flags, cfg.Mode, store); err != nil {
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
			command := upCommand(stacks, prune, revision, refresh)
			eng := engine.New(g, store, nil)
			opts := engine.PlanOptions{Stacks: stacks, Prune: prune, Revision: revision, RefreshRepo: refresh}
			// Plan once without the provider so a bad request fails before
			// one is built, then again with what it supports.
			if _, err := eng.PlanUp(cfg, opts); err != nil {
				return err
			}
			prov, err := newProvider(cfg, homeOf(store), flags.cloudRunOptions(cmd, runOptions{Revision: revision, Timeout: timeout, Stacks: stacks}))
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
			// looks at them. Then the running cluster's version can be
			// read from its node, and must be the pin.
			if err := startStopped(cmd, prov); err != nil {
				return err
			}
			if g, ok := prov.(clusterVersionGate); ok {
				if err := g.CheckClusterVersion(cmd.Context()); err != nil {
					return err
				}
			}
			opts.Supports = prov.Supports
			if sr, ok := prov.(staleReporter); ok {
				if opts.Reapply, err = sr.StaleStacks(cmd.Context()); err != nil {
					return err
				}
			}
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
	cmd.Flags().BoolVar(&refresh, "refresh-repo", false,
		"re-apply the gitops stack so Argo CD reloads its repository Secret from repo.sshKeyFile (a rotated deploy key)")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "cloud mode: lifetime of a new lab (default cloud.ttl in lab.yaml, at most 8h); the reaper deletes it after that")
	registerTimeout(cmd, &timeout)
	cmd.MarkFlagsMutuallyExclusive("stacks", "prune")
	return cmd
}

// restoreDrill puts back a drill that is still in the lab (a crash, a
// reboot, a drill left running) before lab up changes anything: its fault
// is drift, and lab up makes the lab match lab.yaml and Git.
func restoreDrill(cmd *cobra.Command, flags labFlags, mode string, store *state.Store) error {
	if mode != "local" {
		return nil
	}
	st, err := store.Load()
	if err != nil || st.ActiveDrill == nil {
		return err
	}
	m, err := drillsManager(flags)
	if err != nil {
		return fmt.Errorf("a drill is still in the lab, but lab cannot load the drills to put it back: %w", err)
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "a drill is still in the lab (%s); putting everything it changed back first\n", st.ActiveDrill.Phase)
	return drillOp(cmd, m, "lab up (restores the drill)", "restore", func(w io.Writer) error {
		_, _, err := m.RestoreForUp(cmd.Context(), w)
		if err != nil {
			return fmt.Errorf("restoring the drill: %w; lab up changes nothing until it is back (lab drills abort tries again)", err)
		}
		_, _ = fmt.Fprintln(out, "● the drill is restored (lab drills history shows it)")
		return nil
	})
}

// upCommand is the CLI command recorded in the ops log.
func upCommand(stacks []string, prune bool, revision string, refresh bool) string {
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
	if refresh {
		c += " --refresh-repo"
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
