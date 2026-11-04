package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/chanupen/devops-lab/internal/drills"
	"github.com/chanupen/devops-lab/internal/kube"
	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/provider/local"
	"github.com/chanupen/devops-lab/internal/state"
)

// drillsManager builds the drills manager: the drills in drills/ next to
// lab.yaml, the local state file and the main lab's cluster. Drills run in
// local mode. The time-limit timer calls this binary with the same
// --config and --state.
func drillsManager(f labFlags) (*drills.Manager, error) {
	cfgPath, err := f.configPath()
	if err != nil {
		return nil, err
	}
	if cfgPath == "" {
		return nil, errors.New("no lab.yaml in this directory or ~/.lab; pass --config")
	}
	repo, err := repoRoot(cfgPath)
	if err != nil {
		return nil, err
	}
	all, err := drills.LoadAll(filepath.Join(repo, "drills"))
	if err != nil {
		return nil, fmt.Errorf("drills: %w", err)
	}
	path := f.statePath
	if path == "" {
		if path, err = state.PathFor("local"); err != nil {
			return nil, err
		}
	}
	if path, err = filepath.Abs(path); err != nil {
		return nil, err
	}
	kubeconfig := filepath.Join(filepath.Dir(path), "kubeconfig")
	m := &drills.Manager{Drills: all, Store: state.NewStore(path), Kubeconfig: kubeconfig, Runner: labs.ExecRunner{},
		Env: local.LabRegistryEnv(),
		Kube: func() (dynamic.Interface, error) {
			cfg, err := kube.RESTConfig(kubeconfig)
			if err != nil {
				return nil, err
			}
			return dynamic.NewForConfig(cfg)
		}}
	if cfg, err := kube.RESTConfig(kubeconfig); err == nil {
		if c, err := kubernetes.NewForConfig(cfg); err == nil {
			m.Alerts = drills.ProxyAlerts{REST: c.CoreV1().RESTClient()}
		}
	}
	if exe, err := os.Executable(); err == nil {
		absCfg, _ := filepath.Abs(cfgPath)
		m.Timer = drills.SystemdTimer{Binary: exe, Flags: []string{"--config", absCfg, "--state", path}}
	}
	return m, nil
}

// drillOp runs a drill action that changes the cluster (start, verify,
// abort) like lab labs does: output to an op log file and the terminal,
// the op recorded in state.json. A random drill's ID stays out of it.
// Start's output goes to the log only: it would give the fault away.
func drillOp(cmd *cobra.Command, m *drills.Manager, command, action string, fn func(w io.Writer) error) error {
	started := time.Now()
	f, path, err := m.Store.OpenStepLog("drill", "drills-"+action, started)
	if err != nil {
		return fn(cmd.ErrOrStderr())
	}
	_, _ = fmt.Fprintf(f, "$ %s\n", command)
	var w io.Writer = f
	if action != "start" {
		w = io.MultiWriter(cmd.ErrOrStderr(), f)
	}
	runErr := fn(w)
	_ = f.Close()
	op := state.Op{Command: command, Stack: "drill", Action: "drills " + action, Status: "ok", Started: started, Finished: time.Now(), Log: path}
	if runErr != nil {
		op.Status, op.Error = "failed", runErr.Error()
	}
	if err := m.Store.Update(func(s *state.State) error {
		m.Store.RemoveLogs(s.AppendOp(op))
		return nil
	}); err != nil && runErr == nil {
		return err
	}
	if runErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "log: %s\n", path)
	}
	return runErr
}

func newDrillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drills",
		Short: "Break/fix drills: inject a fault, find it, fix it, against the clock",
		Long: "Drills live in drills/ next to lab.yaml and run on the main lab (local mode). Each injects\n" +
			"one fault into the sample app and times how long you take to find it (lab drills ack) and\n" +
			"to fix it (lab drills verify). lab saves what the fault changes before it goes in, and puts\n" +
			"it back when the drill ends, when you abort, at the time limit, or on the next lab up.",
		Args: cobra.NoArgs,
	}
	start, verify, abort := newDrillsStartCmd(), newDrillsVerifyCmd(), newDrillsAbortCmd()
	for _, c := range []*cobra.Command{start, verify, abort} {
		withLabLock(c)
	}
	cmd.AddCommand(newDrillsListCmd(), start, newDrillsStatusCmd(), newDrillsHintCmd(), newDrillsAckCmd(), verify, abort, newDrillsHistoryCmd())
	return cmd
}

func newDrillsListCmd() *cobra.Command {
	var f labFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the drills with your best times",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := drillsManager(f)
			if err != nil {
				return err
			}
			st, err := m.Store.Load()
			if err != nil {
				return err
			}
			return writeDrillList(cmd.OutOrStdout(), m.Drills, st)
		},
	}
	f.register(cmd)
	return cmd
}

func writeDrillList(w io.Writer, all []drills.Drill, st state.State) error {
	best := drills.BestTimes(st.Drills)
	ready := map[string]bool{}
	for _, d := range drills.Eligible(all, st) {
		ready[d.ID] = true
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "\tID\tTITLE\tLEVEL\tLIMIT\tALERT\tRUNS\tBEST DETECT\tBEST RECOVER")
	for _, d := range all {
		mark := "●"
		if !ready[d.ID] {
			mark = "○"
		}
		b := best[d.ID]
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n", mark, d.ID, d.Title, d.Level, shortDuration(d.Limit()),
			orDash(d.Alert.Name), b.Runs, clockOrDash(b.Detect), clockOrDash(b.Fixed))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "\n○ needs stacks that are not up (lab up --stacks <stack>). lab drills start <id>, or --random to practise blind.")
	return err
}

// clock is a duration as 4m12s.
func clock(d time.Duration) string { return d.Round(time.Second).String() }

func clockOrDash(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return clock(d)
}

func newDrillsStartCmd() *cobra.Command {
	var f labFlags
	var random bool
	cmd := &cobra.Command{
		Use:   "start [<id>]",
		Short: "Inject a drill's fault and start the timers",
		Long: "Saves what the fault will change into ~/.lab/state.json, injects the fault and starts the\n" +
			"timers. --random picks a drill whose stacks are up and does not say which, for interview\n" +
			"practice. One drill at a time, and not while a lab is started.",
		Example: "  lab drills start bad-deploy\n  lab drills start --random",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if random == (len(args) == 1) {
				return errors.New("name a drill or pass --random")
			}
			m, err := drillsManager(f)
			if err != nil {
				return err
			}
			id, command := "", "lab drills start --random"
			if !random {
				id, command = args[0], "lab drills start "+args[0]
			}
			var run state.DrillRun
			if err := drillOp(cmd, m, command, "start", func(w io.Writer) error {
				var err error
				run, err = m.Start(cmd.Context(), id, random, w)
				return err
			}); err != nil {
				return err
			}
			return writeDrillStarted(cmd.OutOrStdout(), m, run)
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVar(&random, "random", false, "pick a drill whose stacks are up, and do not say which")
	return cmd
}

// randomPage is what a random drill tells the learner.
const randomPage = "The pager goes off: users of the sample app at https://podinfo.localtest.me:8443 report errors. " +
	"Nobody says what changed. Find what is broken, fix it the way the team runs this cluster (GitOps " +
	"through Argo CD), and run lab drills verify."

func writeDrillStarted(w io.Writer, m *drills.Manager, r state.DrillRun) error {
	d, _ := m.Find(r.Drill)
	var b strings.Builder
	if r.Random {
		b.WriteString("● a random drill started (lab drills history shows which once it ends)\n")
	} else {
		fmt.Fprintf(&b, "● drill %s started: %s\n", d.ID, d.Title)
	}
	fmt.Fprintf(&b, "time limit %s: lab puts everything back on its own at %s\n\n", shortDuration(d.Limit()), r.Deadline.Local().Format("15:04"))
	b.WriteString(drillPage(d, r) + "\n\n")
	fmt.Fprintf(&b, "kubectl: export KUBECONFIG=%s\n", m.Kubeconfig)
	b.WriteString("lab drills ack when you have found the fault (stops the detect timer), lab drills verify once it is fixed,\n" +
		"lab drills hint for a hint, lab drills abort to give up (puts everything back).\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func drillPage(d drills.Drill, r state.DrillRun) string {
	if r.Random {
		return randomPage
	}
	return d.Page
}

func newDrillsStatusCmd() *cobra.Command {
	var f labFlags
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the running drill and its timers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := drillsManager(f)
			if err != nil {
				return err
			}
			r, d, ok, err := m.Active()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !ok {
				_, err := fmt.Fprintln(out, "no drill is running: lab drills list shows them, lab drills start <id> starts one")
				return err
			}
			if r.Phase == state.DrillRunning {
				if c, err := m.CheckAlert(cmd.Context()); err == nil {
					r = c
				}
			}
			_, err = io.WriteString(out, drillStatus(d, r, time.Now()))
			return err
		},
	}
	f.register(cmd)
	return cmd
}

// drillStatus is the running drill with its timers, as status prints it.
func drillStatus(d drills.Drill, r state.DrillRun, now time.Time) string {
	var b strings.Builder
	if r.Random {
		b.WriteString("drill: a random one (lab drills history shows which once it ends)\n")
	} else {
		fmt.Fprintf(&b, "drill %s: %s\n", d.ID, d.Title)
	}
	switch r.Phase {
	case state.DrillInjecting:
		b.WriteString("◐ the fault is going in (if no lab drills start is running, it was interrupted: lab drills abort)\n")
	case state.DrillRestoring:
		b.WriteString("✕ putting the lab back did not finish")
		if r.Error != "" {
			b.WriteString(": " + r.Error)
		}
		b.WriteString("\n  lab drills abort tries again\n")
	default:
		left := r.Deadline.Sub(now)
		if left < 0 {
			fmt.Fprintf(&b, "◐ running %s; the time limit passed: lab drills abort (or the timer) puts everything back\n", clock(now.Sub(r.Started)))
		} else {
			fmt.Fprintf(&b, "◐ running %s, %s left (lab puts everything back at %s)\n", clock(now.Sub(r.Started)), clock(left), r.Deadline.Local().Format("15:04"))
		}
	}
	if r.Alert != "" {
		if r.AlertFired.IsZero() {
			fmt.Fprintf(&b, "  alert %s: not fired yet\n", r.Alert)
		} else {
			fmt.Fprintf(&b, "  alert %s: fired after %s\n", r.Alert, clock(r.TimeTo(r.AlertFired)))
		}
	}
	if r.Acked.IsZero() {
		b.WriteString("  detect: not yet (lab drills ack when you have found it)\n")
	} else {
		fmt.Fprintf(&b, "  detect: %s\n", clock(r.TimeTo(r.Acked)))
	}
	fmt.Fprintf(&b, "  hints: %d of %d, verify runs: %d\n", r.Hints, len(d.Hints), r.Verifies)
	if r.Phase == state.DrillRunning {
		b.WriteString("\n" + drillPage(d, r) + "\n")
	}
	return b.String()
}

func newDrillsHintCmd() *cobra.Command {
	var f labFlags
	cmd := &cobra.Command{
		Use:   "hint",
		Short: "Show the next hint for the running drill",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := drillsManager(f)
			if err != nil {
				return err
			}
			n, total, hint, err := m.Hint()
			if err != nil {
				return err
			}
			if n > total || strings.HasPrefix(hint, "No more hints") {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), hint)
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Hint %d of %d: %s\n", n, total, hint)
			return err
		},
	}
	f.register(cmd)
	return cmd
}

func newDrillsAckCmd() *cobra.Command {
	var f labFlags
	cmd := &cobra.Command{
		Use:   "ack",
		Short: "Say you have found the fault: stops the detect timer",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := drillsManager(f)
			if err != nil {
				return err
			}
			r, err := m.Ack(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "● detected after %s. Now fix it, then lab drills verify.\n", clock(r.TimeTo(r.Acked)))
			if r.Alert != "" && !r.AlertFired.IsZero() {
				_, _ = fmt.Fprintf(out, "  alert %s fired after %s\n", r.Alert, clock(r.TimeTo(r.AlertFired)))
			} else if r.Alert != "" {
				_, _ = fmt.Fprintf(out, "  alert %s has not fired yet: you were faster\n", r.Alert)
			}
			return nil
		},
	}
	f.register(cmd)
	return cmd
}

func newDrillsVerifyCmd() *cobra.Command {
	var f labFlags
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check the fix; a pass stops the recover timer and puts the lab back",
		Long: "Runs the drill's verify script against the main lab (60-second timeout). It only looks.\n" +
			"A pass records the time to recover, then lab puts back everything the drill changed, as\n" +
			"Git has it. Exits 1 when the lab has not recovered yet.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := drillsManager(f)
			if err != nil {
				return err
			}
			var res drills.Result
			err = drillOp(cmd, m, "lab drills verify", "verify", func(w io.Writer) error {
				var err error
				res, err = m.Verify(cmd.Context(), w)
				return err
			})
			out := cmd.OutOrStdout()
			switch {
			case err != nil && !res.Passed:
				return err
			case !res.Passed:
				_, _ = fmt.Fprintf(out, "✕ not recovered yet: %s\nlab drills hint gives a hint.\n", res.Reason)
				return exitError{code: 1}
			}
			_, _ = fmt.Fprintf(out, "● recovered: %s\n", lastOutputLine(res.Output))
			if err != nil {
				_, _ = fmt.Fprintf(out, "✕ putting the lab back failed: %v\n  lab drills abort tries again\n", err)
				return exitError{code: 1}
			}
			return writeDrillResult(out, m, res.Run)
		},
	}
	f.register(cmd)
	return cmd
}

// writeDrillResult prints a finished run's times and the drill's README.
func writeDrillResult(w io.Writer, m *drills.Manager, r state.DrillRun) error {
	d, _ := m.Find(r.Drill)
	var b strings.Builder
	fmt.Fprintf(&b, "\ndrill %s (%s): %s\n", r.Drill, d.Title, r.Outcome)
	b.WriteString(runTimes(r))
	st, err := m.Store.Load()
	if err == nil {
		if best := drills.BestTimes(st.Drills)[r.Drill]; best.Fixed > 0 {
			fmt.Fprintf(&b, "  best: detect %s, recover %s over %d run(s)\n", clockOrDash(best.Detect), clock(best.Fixed), best.Runs)
		}
	}
	fmt.Fprintf(&b, "everything the drill changed is back. What happened and why: %s\n", filepath.Join(r.Dir, "README.md"))
	_, err = io.WriteString(w, b.String())
	return err
}

// runTimes is a run's three times, one per line.
func runTimes(r state.DrillRun) string {
	var alert string
	switch {
	case r.Alert == "":
		alert = "— (no alert named)"
	case !r.AlertFired.IsZero():
		alert = clock(r.TimeTo(r.AlertFired)) + " (" + r.Alert + ")"
	default:
		alert = "did not fire (" + r.Alert + ")"
	}
	return fmt.Sprintf("  time to alert:   %s\n  time to detect:  %s\n  time to recover: %s\n  hints: %d, verify runs: %d\n",
		alert, clockOrDash(r.TimeTo(r.Acked)), clockOrDash(r.TimeTo(r.Recovered)), r.Hints, r.Verifies)
}

func newDrillsAbortCmd() *cobra.Command {
	var f labFlags
	var expired bool
	var run string
	cmd := &cobra.Command{
		Use:   "abort",
		Short: "Give up: put back everything the drill changed",
		Long: "Puts back the objects lab saved before the fault went in, runs the drill's restore.sh and\n" +
			"checks everything is as it was. Safe to run again after a crash or a failed restore.\n" +
			"--expired (used by the time-limit timer) acts only once the drill's time is up.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := drillsManager(f)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if expired {
				var done bool
				err := drillOp(cmd, m, "lab drills abort --expired", "expire", func(w io.Writer) error {
					var err error
					done, err = m.Expire(cmd.Context(), run, w)
					return err
				})
				if err == nil && done {
					_, _ = fmt.Fprintln(out, "the drill's time limit passed: everything it changed is back")
				}
				return err
			}
			if _, _, ok, err := m.Active(); err != nil || !ok {
				if err == nil {
					_, err = fmt.Fprintln(out, "no drill is running")
				}
				return err
			}
			var r state.DrillRun
			if err := drillOp(cmd, m, "lab drills abort", "abort", func(w io.Writer) error {
				var err error
				r, err = m.Abort(cmd.Context(), w)
				return err
			}); err != nil {
				return err
			}
			return writeDrillResult(out, m, r)
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVar(&expired, "expired", false, "restore only if the drill's time limit has passed (the timer uses it)")
	cmd.Flags().StringVar(&run, "run", "", "with --expired: only this run")
	return cmd
}

func newDrillsHistoryCmd() *cobra.Command {
	var f labFlags
	cmd := &cobra.Command{
		Use:   "history",
		Short: "List finished drill runs and the best times per drill",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := f.statePath
			if path == "" {
				var err error
				if path, err = state.PathFor("local"); err != nil {
					return err
				}
			}
			st, err := state.NewStore(path).Load()
			if err != nil {
				return err
			}
			return writeDrillHistory(cmd.OutOrStdout(), st.Drills)
		},
	}
	f.register(cmd)
	return cmd
}

func writeDrillHistory(w io.Writer, runs []state.DrillRun) error {
	if len(runs) == 0 {
		_, err := fmt.Fprintln(w, "no drill has finished yet: lab drills start <id>")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "STARTED\tDRILL\tOUTCOME\tALERT\tDETECT\tRECOVER\tHINTS")
	for _, r := range runs {
		id := r.Drill
		if r.Random {
			id += " (random)"
		}
		alert := "—"
		if !r.AlertFired.IsZero() {
			alert = clock(r.TimeTo(r.AlertFired))
		} else if r.Alert != "" && !r.Started.IsZero() {
			alert = "did not fire"
		}
		started := r.Started
		if started.IsZero() {
			started = r.Ended
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n", started.Local().Format("Jan 02 15:04"), id, r.Outcome, alert,
			clockOrDash(r.TimeTo(r.Acked)), clockOrDash(r.TimeTo(r.Recovered)), r.Hints)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w, "\nBest (recovered runs):")
	best := drills.BestTimes(runs)
	tw = tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  DRILL\tRUNS\tRECOVERED\tALERT\tDETECT\tRECOVER")
	ids := make([]string, 0, len(best))
	for id := range best {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		b := best[id]
		_, _ = fmt.Fprintf(tw, "  %s\t%d\t%d\t%s\t%s\t%s\n", id, b.Runs, b.Recovered, clockOrDash(b.Alert), clockOrDash(b.Detect), clockOrDash(b.Fixed))
	}
	return tw.Flush()
}
