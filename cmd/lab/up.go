package main

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/engine"
	labaws "github.com/chanupen/devops-lab/internal/provider/aws"
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
			if err := cfg.CheckBuilt(); err != nil {
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
			opts := engine.PlanOptions{Stacks: stacks, Prune: prune, Revision: revision, RefreshRepo: refresh}
			return executeUp(cmd, newProvider, flags, cfg, store, g, opts, timeout, command, yes, nil)
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

// executeUp is the shared consented apply path. Start supplies the plan it
// confirmed; ordinary up retains its existing planning/confirmation behavior.
func executeUp(cmd *cobra.Command, factory providerFactory, flags labFlags, cfg config.Config, store *state.Store,
	g *engine.Graph, opts engine.PlanOptions, timeout time.Duration, command string, yes bool, expected *engine.Plan,
) error {
	if err := cmd.Context().Err(); err != nil {
		return err
	}
	eng := engine.New(g, store, nil)
	if _, err := eng.PlanUp(cfg, opts); err != nil {
		return err
	}
	prov, err := factory(cfg, homeOf(store), flags.cloudRunOptions(cmd, runOptions{Revision: opts.Revision, Timeout: timeout, Stacks: opts.Stacks}))
	if err != nil {
		return err
	}
	if gate, ok := prov.(versionGate); ok {
		if err := gate.CheckK8sVersion(cmd.Context()); err != nil {
			return err
		}
	}
	if err := startStopped(cmd, prov); err != nil {
		return err
	}
	if gate, ok := prov.(clusterVersionGate); ok {
		if err := gate.CheckClusterVersion(cmd.Context()); err != nil {
			return err
		}
	}
	p, err := observeUpPlan(cmd.Context(), prov, eng, cfg, opts)
	if err != nil {
		return err
	}
	if gate, ok := prov.(stackVersionGate); ok {
		if err := gate.CheckNewStacks(cmd.Context(), newStacks(p, store)); err != nil {
			return err
		}
	}
	out := cmd.OutOrStdout()
	if err := writePlan(out, lipgloss.NewRenderer(out), p); err != nil {
		return err
	}
	if expected != nil && !reflect.DeepEqual(p, *expected) {
		return fmt.Errorf("live discovery changed the confirmed plan; containers may have restarted, but no stacks were applied; run lab start again")
	}
	if err := cmd.Context().Err(); err != nil {
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
		if err := prefetchCharts(cmd.Context(), prov, p, out); err != nil {
			return err
		}
		if err := run(cmd, prov, g, store, p, command); err != nil {
			warnHalfBuilt(cmd, flags, store)
			return err
		}
	}
	return printApps(cmd, prov, store)
}

// observeUpPlan never starts containers, fetches charts or runs a stack.
func observeUpPlan(ctx context.Context, prov provider, eng *engine.Engine, cfg config.Config, opts engine.PlanOptions) (engine.Plan, error) {
	opts.Supports = prov.Supports
	if sr, ok := prov.(staleReporter); ok {
		var err error
		if opts.Reapply, err = sr.StaleStacks(ctx); err != nil {
			return engine.Plan{}, err
		}
	}
	return eng.PlanUp(cfg, opts)
}

// restoreDrill puts back a drill that is still in the lab (a crash, a
// reboot, a drill left running) before lab up changes anything: its fault
// is drift, and lab up makes the lab match lab.yaml and Git. That includes
// a drill an interrupted e2e test run left in e2e/state.json beside the
// state file; a running e2e test holds the lab lock, so lab up never gets
// here while it runs.
func restoreDrill(cmd *cobra.Command, flags labFlags, mode string, store *state.Store) error {
	if mode != "local" {
		return nil
	}
	e2e := flags
	e2e.statePath = state.E2EPath(store.Path())
	for _, c := range []struct {
		flags labFlags
		store *state.Store
		what  string
	}{
		{flags, store, "a drill"},
		{e2e, state.NewStore(e2e.statePath), "an e2e test run's drill"},
	} {
		if err := restoreOneDrill(cmd, c.flags, c.store, c.what); err != nil {
			return err
		}
	}
	return nil
}

func restoreOneDrill(cmd *cobra.Command, flags labFlags, store *state.Store, what string) error {
	st, err := store.Load()
	if err != nil || st.ActiveDrill == nil {
		return err
	}
	m, err := drillsManager(flags)
	if err != nil {
		return fmt.Errorf("%s is still in the lab, but lab cannot load the drills to put it back: %w", what, err)
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "%s is still in the lab (%s); putting everything it changed back first\n", what, st.ActiveDrill.Phase)
	return drillOp(cmd, m, "lab up (restores the drill)", "restore", func(w io.Writer) error {
		_, _, err := m.RestoreForUp(cmd.Context(), w)
		if err != nil {
			return fmt.Errorf("restoring the drill: %w; lab up changes nothing until it is back (lab drills abort --state %s tries again)", err, store.Path())
		}
		_, _ = fmt.Fprintln(out, "● the drill is restored")
		return nil
	})
}

// stackVersionGate is implemented by providers whose running cluster keeps
// its Kubernetes version (cloud): a stack added to it must support that
// version.
type stackVersionGate interface {
	CheckNewStacks(ctx context.Context, stacks []string) error
}

// newStacks are the stacks plan p applies that the state does not record
// as applied: the ones being added, not re-applied.
func newStacks(p engine.Plan, store *state.Store) []string {
	st, err := store.Load()
	if err != nil {
		st = state.State{}
	}
	var out []string
	for _, s := range p.Steps {
		if s.Action == engine.Apply && !st.IsApplied(s.Stack) {
			out = append(out, s.Stack)
		}
	}
	return out
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

// warnHalfBuilt tells the learner, in red, that a cloud lab up stopped
// with AWS resources still running, and the command that removes them. A
// failed apply is never left to the TTL silently: the reaper is the dead
// man's switch, not the plan.
func warnHalfBuilt(cmd *cobra.Command, flags labFlags, store *state.Store) {
	st, err := store.Load()
	if err != nil || len(st.HalfBuilt()) == 0 {
		return
	}
	out := cmd.ErrOrStderr()
	red := lipgloss.NewRenderer(out).NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	until := "the reaper removes it after expires-at"
	if exp, err := labaws.ReadExpiry(labaws.ExpiryFile(homeOf(store))); err == nil {
		until = "the reaper removes it after expires-at " + exp.Local().Format("2006-01-02 15:04 MST")
	}
	_, _ = fmt.Fprintf(out, "\n%s\n%s\n",
		red.Render(fmt.Sprintf("lab up failed (%s): the cloud lab is half built and still billed; %s.",
			strings.Join(st.HalfBuilt(), ", "), until)),
		red.Render("run now: "+cloudDownCommand(flags)))
}

// cloudDownCommand is the cloud lab down command for the same config and state as
// this one.
func cloudDownCommand(flags labFlags) string {
	c := "lab down --mode cloud"
	if flags.cfgPath != "" {
		c += " --config " + flags.cfgPath
	}
	if flags.statePath != "" {
		c += " --state " + flags.statePath
	}
	return c
}

// prefetchCharts fetches the Helm charts the plan's applies install from
// the host, before the first stack runs. A chart repository that does not
// answer then fails lab up with nothing built, instead of after the
// cluster stack has run for ten minutes.
func prefetchCharts(ctx context.Context, prov provider, p engine.Plan, out io.Writer) error {
	pf, ok := prov.(chartPrefetcher)
	if !ok {
		return nil
	}
	var stacks []string
	for _, s := range p.Steps {
		if s.Action == engine.Apply {
			stacks = append(stacks, s.Stack)
		}
	}
	return pf.PrefetchCharts(ctx, stacks, out)
}
