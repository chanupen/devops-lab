package main

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/provider/local"
	"github.com/chanupen/devops-lab/internal/state"
)

// labsFlags locate lab.yaml (and so the labs/ directory beside it) and the
// state file that keeps lab progress.
type labsFlags struct {
	labFlags
	yes bool
}

// manager builds the labs manager. Labs run in local mode; progress is in
// the local state file (~/.lab/state.json). Commands that make or delete
// scratch clusters pass scratch and need Podman; the others wire it when it
// is there, only to read how long a lab's cluster has run.
func (f *labsFlags) manager(scratch bool) (*labs.Manager, error) {
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
	all, err := labs.LoadAll(filepath.Join(repo, "labs"))
	if err != nil {
		return nil, fmt.Errorf("labs: %w", err)
	}
	path := f.statePath
	if path == "" {
		if path, err = state.PathFor("local"); err != nil {
			return nil, err
		}
	}
	home := filepath.Dir(path)
	m := &labs.Manager{Labs: all, Store: state.NewStore(path), Mode: "local", Runner: labs.ExecRunner{}, Home: home,
		MainKubeconfig: filepath.Join(home, "kubeconfig"), LookPath: exec.LookPath,
		// The lab registry, from the host and from the cluster, and the
		// Secrets Manager endpoint (Moto) with dummy keys. A cloud overlay
		// sets ECR and drops the endpoint; the lab scripts read only these.
		Env:     append(local.LabRegistryEnv(), local.MotoEnv()...),
		Prepare: func(labs.Lab) error { return local.EnsureLabRegistry(home) }}
	// Scratch clusters run lab.yaml's k8sVersion, like the main lab.
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	s, err := local.NewScratch(home, cfg.K8sVersion)
	switch {
	case err == nil:
		m.Clusters = s
	case scratch:
		return nil, err
	}
	return m, nil
}

// labOp runs a lab action that changes things (start, stop, reset) the way
// lab up runs a stack: its output goes to an op log file and the op is
// recorded in state.json, so the Ops screen and the log keep what happened,
// including a failed cluster's diagnostics. The terminal gets the progress
// lines; kind's own output and diagnostics only with verbose.
func labOp(cmd *cobra.Command, m *labs.Manager, command, lab, action string, verbose bool, fn func(w io.Writer) error) error {
	started := time.Now()
	f, path, err := m.Store.OpenStepLog(lab, "labs-"+action, started)
	if err != nil {
		return fn(cmd.ErrOrStderr())
	}
	_, _ = fmt.Fprintf(f, "$ %s\n", command)
	runErr := fn(newOpLog(cmd.ErrOrStderr(), f, verbose))
	_ = f.Close()
	op := state.Op{Command: command, Stack: lab, Action: "labs " + action, Status: "ok", Started: started, Finished: time.Now(), Log: path}
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

// opLog writes progress to the terminal and the op log file, and detail
// (local.Detailer) to the file alone, or to both when verbose.
type opLog struct {
	io.Writer
	detail io.Writer
}

func newOpLog(terminal, file io.Writer, verbose bool) opLog {
	both := io.MultiWriter(terminal, file)
	if verbose {
		return opLog{Writer: both, detail: both}
	}
	return opLog{Writer: both, detail: file}
}

// Detail implements local.Detailer.
func (o opLog) Detail() io.Writer { return o.detail }

// registerVerbose adds -v/--verbose to a lab action that runs kind.
func registerVerbose(cmd *cobra.Command, v *bool) {
	cmd.Flags().BoolVarP(v, "verbose", "v", false, "stream kind's full output too (it is always in the op log)")
}

func newLabsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "labs",
		Short: "Hands-on labs: list, start, verify each step, stop",
		Long: "Labs live in labs/ next to lab.yaml. A scratch lab gets its own single-node kind cluster,\n" +
			"lab-scratch-<id>, which lab labs stop deletes with the lab's progress; the main lab is not\n" +
			"touched. Progress is kept in ~/.lab/state.json.",
		Args: cobra.NoArgs,
	}
	start, stop, reset := newLabsStartCmd(), newLabsStopCmd(), newLabsResetCmd()
	for _, c := range []*cobra.Command{start, stop, reset} {
		withLabLock(c)
	}
	cmd.AddCommand(newLabsListCmd(), start, newLabsStatusCmd(), newLabsVerifyCmd(), newLabsHintCmd(), newLabsSolutionCmd(), reset, stop)
	return cmd
}

func newLabsListCmd() *cobra.Command {
	var f labsFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the labs with your progress",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := f.manager(false)
			if err != nil {
				return err
			}
			st, err := m.Store.Load()
			if err != nil {
				return err
			}
			return writeLabList(cmd.OutOrStdout(), m.Labs, st, m.TimeSpent)
		},
	}
	f.register(cmd)
	return cmd
}

// spentFunc is a lab's time spent; Manager.TimeSpent asks its cluster.
type spentFunc func(labs.Lab, state.LabProgress) time.Duration

func writeLabList(w io.Writer, all []labs.Lab, st state.State, spent spentFunc) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "\tID\tTITLE\tTIME\tLEVEL\tNEEDS\tPROGRESS")
	for _, l := range all {
		p := st.Labs[l.ID]
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", labs.LabMark(l, p).Symbol(), l.ID, l.Title, l.Time, l.Level, l.Needs,
			progressText(l, p, st.ActiveLab == l.ID, spent(l, p)))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "\nlab labs start <id> starts one (the number is enough: lab labs start 1).")
	return err
}

// progressText is "step 2/6, 15m, started", or "done, 6/6 steps, 42m".
func progressText(l labs.Lab, p state.LabProgress, active bool, spent time.Duration) string {
	if p.Step == 0 {
		return "not started"
	}
	var s string
	if p.Step > len(l.Steps) {
		s = fmt.Sprintf("done, %d/%d steps, %s", len(l.Steps), len(l.Steps), shortDuration(spent))
	} else {
		s = fmt.Sprintf("step %d/%d, %s", p.Step, len(l.Steps), shortDuration(spent))
	}
	if active {
		s += ", started"
	}
	return s
}

func shortDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func newLabsStartCmd() *cobra.Command {
	var f labsFlags
	var verbose bool
	cmd := &cobra.Command{
		Use:     "start <id>",
		Short:   "Start a lab: make its cluster ready and pick up at your step",
		Example: "  lab labs start 1\n  lab labs start 01-argocd-bootstrap\n  lab labs start 1 -v   # stream kind's full output",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := f.manager(true)
			if err != nil {
				return err
			}
			l, err := m.Find(args[0])
			if err != nil {
				return err
			}
			if err := labOp(cmd, m, "lab labs start "+l.Number(), l.ID, "start", verbose, func(w io.Writer) error {
				_, err := m.Start(cmd.Context(), l.ID, w)
				return err
			}); err != nil {
				return err
			}
			return writeLabStatus(cmd.OutOrStdout(), m)
		},
	}
	f.register(cmd)
	registerVerbose(cmd, &verbose)
	return cmd
}

func newLabsStatusCmd() *cobra.Command {
	var f labsFlags
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the started lab: its steps, your progress and the current task",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := f.manager(false)
			if err != nil {
				return err
			}
			return writeLabStatus(cmd.OutOrStdout(), m)
		},
	}
	f.register(cmd)
	return cmd
}

func writeLabStatus(w io.Writer, m *labs.Manager) error {
	l, p, ok, err := m.Active()
	if err != nil {
		return err
	}
	if !ok {
		_, err := fmt.Fprintln(w, "no lab is started: lab labs list shows them, lab labs start <id> starts one")
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "lab %s: %s\n", l.ID, l.Title)
	if l.Needs == labs.NeedsScratch {
		fmt.Fprintf(&b, "cluster: %s (its own; the main lab is not touched)\n", m.Scratch(l))
	} else {
		b.WriteString("cluster: the main lab\n")
	}
	fmt.Fprintf(&b, "kubectl: export KUBECONFIG=%s\n", m.Kubeconfig(l))
	fmt.Fprintf(&b, "time spent: %s of about %s\n\n", shortDuration(m.TimeSpent(l, p)), l.Time)
	for _, s := range l.Steps {
		fmt.Fprintf(&b, "  %s %d  %s%s\n", labs.StepMark(p, s.N).Symbol(), s.N, s.Title, stepNotes(p, s.N))
	}
	if cur, ok := l.Step(p.Step); ok {
		fmt.Fprintf(&b, "\nStep %d: %s\n\n%s\n\nExpected result: %s\n\nlab labs verify checks it; lab labs hint gives a hint.\n",
			cur.N, cur.Title, cur.Task, cur.Expected)
	} else {
		b.WriteString("\nAll steps are done. lab labs stop deletes the lab's cluster.\n")
	}
	_, err = io.WriteString(w, b.String())
	return err
}

// stepNotes is ", 3 attempts, 1 hint, solution shown" for a step.
func stepNotes(p state.LabProgress, n int) string {
	var notes []string
	if a := p.Attempts[n]; a > 0 {
		notes = append(notes, plural(a, "attempt"))
	}
	if h := p.Hints[n]; h > 0 {
		notes = append(notes, plural(h, "hint"))
	}
	for _, s := range p.Solutions {
		if s == n {
			notes = append(notes, "solution shown")
		}
	}
	if len(notes) == 0 {
		return ""
	}
	return "  (" + strings.Join(notes, ", ") + ")"
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// stepArg reads an optional step number; 0 means the current step.
func stepArg(args []string) (int, error) {
	if len(args) == 0 {
		return 0, nil
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("step %q is not a step number", args[0])
	}
	return n, nil
}

func newLabsVerifyCmd() *cobra.Command {
	var f labsFlags
	cmd := &cobra.Command{
		Use:   "verify [step]",
		Short: "Check a step (default: the current one); passing it moves you on",
		Long:  "Runs the step's verify script against the lab's cluster, with a 60-second timeout.\nIt only looks; it never changes anything. Exits 1 when the step is not done yet.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := stepArg(args)
			if err != nil {
				return err
			}
			m, err := f.manager(false)
			if err != nil {
				return err
			}
			l, res, err := m.Verify(cmd.Context(), n)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			s, _ := l.Step(res.Step)
			if !res.Passed {
				_, _ = fmt.Fprintf(out, "✕ step %d, %s: not done yet\n  %s\nlab labs hint gives a hint.\n", s.N, s.Title, res.Reason)
				return exitError{code: 1}
			}
			_, _ = fmt.Fprintf(out, "● step %d, %s: done (%s)\n  %s\n", s.N, s.Title, res.Duration.Round(100*time.Millisecond), lastOutputLine(res.Output))
			_, p, _, _ := m.Active()
			if next, ok := l.Step(p.Step); ok {
				_, _ = fmt.Fprintf(out, "next: step %d, %s (lab labs status shows the task)\n", next.N, next.Title)
			} else if res.Step == len(l.Steps) {
				_, _ = fmt.Fprintf(out, "Lab %s is done in %s. lab labs stop deletes its cluster.\n", l.Number(), shortDuration(m.TimeSpent(l, p)))
			}
			return nil
		},
	}
	f.register(cmd)
	return cmd
}

func lastOutputLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func newLabsHintCmd() *cobra.Command {
	var f labsFlags
	cmd := &cobra.Command{
		Use:   "hint [step]",
		Short: "Show the next hint for a step (default: the current one)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := stepArg(args)
			if err != nil {
				return err
			}
			m, err := f.manager(false)
			if err != nil {
				return err
			}
			_, step, hint, err := m.Hint(n)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Step %d. %s\n", step, hint)
			return err
		},
	}
	f.register(cmd)
	return cmd
}

func newLabsSolutionCmd() *cobra.Command {
	var f labsFlags
	var id string
	cmd := &cobra.Command{
		Use:   "solution <step>",
		Short: "Show a step's reference answer (asks first)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := stepArg(args)
			if err != nil {
				return err
			}
			m, err := f.manager(false)
			if err != nil {
				return err
			}
			if err := confirm(cmd, f.yes, fmt.Sprintf("Show the answer to step %d? Trying first teaches more", n)); err != nil {
				return err
			}
			_, text, err := m.Solution(id, n)
			if err != nil {
				return err
			}
			_, err = io.WriteString(cmd.OutOrStdout(), text)
			return err
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "do not ask")
	cmd.Flags().StringVar(&id, "lab", "", "lab id (default: the started lab)")
	return cmd
}

func newLabsResetCmd() *cobra.Command {
	var f labsFlags
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Return the started lab to its start (asks first)",
		Long:  "Runs the lab's reset.sh against its cluster and starts the steps over. Time spent is kept.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := f.manager(false)
			if err != nil {
				return err
			}
			l, _, ok, err := m.Active()
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("no lab is started: lab labs start <id>")
			}
			if err := confirm(cmd, f.yes, fmt.Sprintf("Reset lab %s? What you built in its cluster is deleted", l.ID)); err != nil {
				return err
			}
			if err := labOp(cmd, m, "lab labs reset", l.ID, "reset", true, func(w io.Writer) error {
				_, err := m.Reset(cmd.Context(), w)
				return err
			}); err != nil {
				return err
			}
			return writeLabStatus(cmd.OutOrStdout(), m)
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "do not ask")
	return cmd
}

// stopMessage says how far the lab got: "lab 01-x stopped at step 4/6
// after 32m", then what the next start does.
func stopMessage(l labs.Lab, p state.LabProgress) string {
	n := len(l.Steps)
	msg := fmt.Sprintf("lab %s stopped at step %d/%d after %s", l.ID, min(p.Step, n), n, shortDuration(p.Spent))
	if p.Step > n {
		msg = fmt.Sprintf("lab %s stopped with all %d steps done after %s", l.ID, n, shortDuration(p.Spent))
	}
	if l.Needs == labs.NeedsScratch {
		return msg + fmt.Sprintf("; its cluster is deleted, so lab labs start %s begins at step 1", l.Number())
	}
	return msg + fmt.Sprintf("; lab labs start %s picks up at step %d", l.Number(), min(p.Step, n))
}

func newLabsStopCmd() *cobra.Command {
	var f labsFlags
	var verbose bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the started lab; a scratch lab's cluster and progress are deleted",
		Long: "Deletes a scratch lab's cluster, and with it the lab's steps and time: the next start\n" +
			"begins at step 1 on a new cluster. A lab on the main cluster keeps its steps; its time\n" +
			"starts from zero.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := f.manager(true)
			if err != nil {
				return err
			}
			l, _, ok, err := m.Active()
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("no lab is started: lab labs start <id>")
			}
			var reached state.LabProgress
			if err := labOp(cmd, m, "lab labs stop", l.ID, "stop", verbose, func(w io.Writer) error {
				var err error
				_, reached, err = m.Stop(cmd.Context(), w)
				return err
			}); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), stopMessage(l, reached))
			return err
		},
	}
	f.register(cmd)
	registerVerbose(cmd, &verbose)
	return cmd
}
