package drills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os/exec"
	"slices"
	"strings"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/state"
)

// Script timeouts: verify like a lab step; inject and restore wait for
// rollouts.
const (
	VerifyTimeout = 60 * time.Second
	ScriptTimeout = 10 * time.Minute
)

// Manager starts drills, times them and puts the lab back afterwards. The
// state file holds the active run and its snapshot, so any lab process can
// restore it, even after a crash or a reboot.
type Manager struct {
	Drills []Drill
	Store  *state.Store
	// Kube connects to the main lab's cluster.
	Kube       func() (dynamic.Interface, error)
	Kubeconfig string
	Runner     labs.Runner
	Alerts     Alerts // nil: alerts are not checked
	Timer      Timer  // nil: only lab's own checks restore an expired drill
	// Env is added to every script's environment (the lab registry).
	Env []string
	Now func() time.Time
	// Pick chooses a random drill among n; nil is math/rand.
	Pick func(n int) int
	// VerifyTimeout and ScriptTimeout override the defaults (tests).
	VerifyTimeout, ScriptTimeout time.Duration
}

func (m *Manager) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}

// Find returns the drill with this ID.
func (m *Manager) Find(id string) (Drill, error) {
	for _, d := range m.Drills {
		if d.ID == id {
			return d, nil
		}
	}
	return Drill{}, fmt.Errorf("no drill %q: lab drills list shows them", id)
}

// Active returns the active run and its drill; ok is false when none runs.
// A drill no longer in drills/ still restores from the directory recorded
// in the run.
func (m *Manager) Active() (state.DrillRun, Drill, bool, error) {
	st, err := m.Store.Load()
	if err != nil || st.ActiveDrill == nil {
		return state.DrillRun{}, Drill{}, false, err
	}
	r := *st.ActiveDrill
	d, err := m.Find(r.Drill)
	if err != nil {
		d, err = Load(r.Dir)
		if err != nil {
			d = Drill{ID: r.Drill, Dir: r.Dir}
		}
	}
	return r, d, true, nil
}

func (m *Manager) active() (state.DrillRun, Drill, error) {
	r, d, ok, err := m.Active()
	if err == nil && !ok {
		err = errors.New("no drill is running: lab drills start <id>, or lab drills start --random")
	}
	return r, d, err
}

// Eligible are the drills whose stacks are all applied.
func Eligible(all []Drill, st state.State) []Drill {
	var out []Drill
	for _, d := range all {
		if !slices.ContainsFunc(d.Stacks, func(s string) bool { return !st.IsApplied(s) }) {
			out = append(out, d)
		}
	}
	return out
}

// Start injects drill id's fault (a random eligible drill when random)
// and starts its timers. What restore needs is in the state file before
// the fault goes in; a failed inject is restored at once.
func (m *Manager) Start(ctx context.Context, id string, random bool, log io.Writer) (state.DrillRun, error) {
	st, err := m.Store.Load()
	if err != nil {
		return state.DrillRun{}, err
	}
	switch {
	case st.ActiveDrill != nil:
		return state.DrillRun{}, fmt.Errorf("a drill is running (%s); one at a time: lab drills verify, or lab drills abort", label(*st.ActiveDrill))
	case st.ActiveLab != "":
		return state.DrillRun{}, fmt.Errorf("lab %s is started; drills run on the main lab, so stop it first: lab labs stop", st.ActiveLab)
	}
	var d Drill
	if random {
		pool := Eligible(m.Drills, st)
		if len(pool) == 0 {
			return state.DrillRun{}, errors.New("no drill can run: each needs its stacks up (lab drills list shows them)")
		}
		pick := m.Pick
		if pick == nil {
			pick = rand.IntN
		}
		d = pool[pick(len(pool))]
	} else {
		if d, err = m.Find(id); err != nil {
			return state.DrillRun{}, err
		}
		var down []string
		for _, s := range d.Stacks {
			if !st.IsApplied(s) {
				down = append(down, s)
			}
		}
		if len(down) > 0 {
			return state.DrillRun{}, fmt.Errorf("drill %s needs %s: lab up --stacks %s", d.ID, strings.Join(down, ", "), strings.Join(down, ","))
		}
	}
	dyn, err := m.Kube()
	if err != nil {
		return state.DrillRun{}, err
	}
	saved, err := Capture(ctx, dyn, d.Snapshot)
	if err != nil {
		return state.DrillRun{}, err
	}
	now := m.now()
	run := state.DrillRun{
		Run: now.UTC().Format("20060102T150405Z"), Drill: d.ID, Dir: d.Dir, Random: random,
		Phase: state.DrillInjecting, Deadline: now.Add(d.Limit()), Alert: d.Alert.Name, Snapshot: saved,
	}
	// Written before anything changes: from here on, abort, lab up and the
	// timer can put the lab back.
	if err := m.Store.Update(func(s *state.State) error {
		if s.ActiveDrill != nil {
			return fmt.Errorf("a drill is running (%s)", label(*s.ActiveDrill))
		}
		s.ActiveDrill = &run
		return nil
	}); err != nil {
		return state.DrillRun{}, err
	}
	if m.Timer != nil {
		unit, err := m.Timer.Schedule(ctx, run.Run, run.Deadline)
		if err != nil {
			logf(log, "warning: no timer restores the lab at the time limit (%v); lab drills abort, lab up or an open TUI will", err)
		} else {
			run.Timer = unit
		}
	}
	logf(log, "injecting the fault")
	out, err := m.script(ctx, d, run, "inject", m.scriptTimeout())
	_, _ = io.WriteString(log, out)
	if err != nil {
		logf(log, "inject.sh failed (%v); putting everything back", err)
		if rerr := m.restore(ctx, d, &run, state.DrillInjectFailed, log); rerr != nil {
			return run, fmt.Errorf("inject.sh: %w; restore also failed: %w (lab drills abort tries again)", err, rerr)
		}
		return run, fmt.Errorf("inject.sh: %w (everything is back as it was)", err)
	}
	run.Phase, run.Started = state.DrillRunning, m.now()
	return run, m.Store.Update(func(s *state.State) error {
		if s.ActiveDrill == nil || s.ActiveDrill.Run != run.Run {
			return errors.New("the drill was restored while it was injecting")
		}
		s.ActiveDrill.Phase, s.ActiveDrill.Started, s.ActiveDrill.Timer = run.Phase, run.Started, run.Timer
		return nil
	})
}

// label names a run without giving a random drill away.
func label(r state.DrillRun) string {
	if r.Random {
		return "a random drill"
	}
	return "drill " + r.Drill
}

func (m *Manager) scriptTimeout() time.Duration {
	if m.ScriptTimeout > 0 {
		return m.ScriptTimeout
	}
	return ScriptTimeout
}

// script runs one of d's scripts against the main cluster.
func (m *Manager) script(ctx context.Context, d Drill, r state.DrillRun, name string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	env := append([]string{"KUBECONFIG=" + m.Kubeconfig, "DRILL_ID=" + d.ID, "DRILL_DIR=" + d.Dir, "DRILL_RUN=" + r.Run}, m.Env...)
	out, err := m.Runner.Run(ctx, d.Script(name), d.Dir, env)
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("timed out after %s", timeout)
	}
	return out, err
}

// CheckAlert records when the drill's alert first fired, if it has, and
// returns the run as it is now.
func (m *Manager) CheckAlert(ctx context.Context) (state.DrillRun, error) {
	r, d, err := m.active()
	if err != nil || m.Alerts == nil || d.Alert.Name == "" || !r.AlertFired.IsZero() || r.Started.IsZero() {
		return r, err
	}
	until := r.Recovered
	at, ok, err := m.Alerts.FiredAt(ctx, d.Alert, r.Started)
	if err != nil || !ok || (!until.IsZero() && at.After(until)) {
		return r, err
	}
	r.AlertFired = at
	return r, m.Store.Update(func(s *state.State) error {
		if s.ActiveDrill != nil && s.ActiveDrill.Run == r.Run && s.ActiveDrill.AlertFired.IsZero() {
			s.ActiveDrill.AlertFired = at
		}
		return nil
	})
}

// Ack records that the learner found the fault: it stops the detect timer.
func (m *Manager) Ack(ctx context.Context) (state.DrillRun, error) {
	r, _, err := m.active()
	if err != nil {
		return r, err
	}
	if r.Phase != state.DrillRunning {
		return r, fmt.Errorf("the drill is %s", r.Phase)
	}
	if !r.Acked.IsZero() {
		return r, nil
	}
	r.Acked = m.now()
	if err := m.Store.Update(func(s *state.State) error {
		if s.ActiveDrill != nil && s.ActiveDrill.Run == r.Run && s.ActiveDrill.Acked.IsZero() {
			s.ActiveDrill.Acked = r.Acked
		}
		return nil
	}); err != nil {
		return r, err
	}
	if c, err := m.CheckAlert(ctx); err == nil {
		r = c
	}
	return r, nil
}

// Hint returns the next hint and counts it.
func (m *Manager) Hint() (n, total int, hint string, err error) {
	r, d, err := m.active()
	if err != nil {
		return 0, 0, "", err
	}
	if r.Hints >= len(d.Hints) {
		return r.Hints, len(d.Hints), "No more hints. drills/" + d.ID + "/solution.sh is the reference fix; lab drills abort puts everything back.", nil
	}
	hint = d.Hints[r.Hints]
	return r.Hints + 1, len(d.Hints), hint, m.Store.Update(func(s *state.State) error {
		if s.ActiveDrill != nil && s.ActiveDrill.Run == r.Run {
			s.ActiveDrill.Hints = r.Hints + 1
		}
		return nil
	})
}

// Result is one verify run.
type Result struct {
	Passed bool
	Reason string // why not, from the script's last line
	Output string
	Run    state.DrillRun
}

// Verify runs the drill's verify script. A pass stops the recover timer,
// then puts the lab back as it was before the drill (the learner's fix
// may leave it working, but not as Git has it) and ends the run.
func (m *Manager) Verify(ctx context.Context, log io.Writer) (Result, error) {
	r, d, err := m.active()
	if err != nil {
		return Result{}, err
	}
	if r.Phase != state.DrillRunning {
		return Result{Run: r}, fmt.Errorf("the drill is %s; lab drills abort puts everything back", r.Phase)
	}
	timeout := m.VerifyTimeout
	if timeout == 0 {
		timeout = VerifyTimeout
	}
	out, verr := m.script(ctx, d, r, "verify", timeout)
	res := Result{Passed: verr == nil, Output: out}
	var exit *exec.ExitError
	switch {
	case verr == nil:
	case errors.As(verr, &exit):
		res.Reason = lastLine(out)
		if res.Reason == "" {
			res.Reason = fmt.Sprintf("exit status %d", exit.ExitCode())
		}
	default:
		res.Reason = verr.Error()
	}
	now := m.now()
	if err := m.Store.Update(func(s *state.State) error {
		if s.ActiveDrill == nil || s.ActiveDrill.Run != r.Run {
			return errors.New("the drill ended while verify ran")
		}
		s.ActiveDrill.Verifies++
		if res.Passed && s.ActiveDrill.Recovered.IsZero() {
			s.ActiveDrill.Recovered = now
		}
		r = *s.ActiveDrill
		return nil
	}); err != nil {
		return res, err
	}
	res.Run = r
	if !res.Passed {
		return res, nil
	}
	if c, err := m.CheckAlert(ctx); err == nil {
		r = c
	}
	err = m.restore(ctx, d, &r, state.DrillRecovered, log)
	res.Run = r
	return res, err
}

// Abort puts everything back and ends the run.
func (m *Manager) Abort(ctx context.Context, log io.Writer) (state.DrillRun, error) {
	return m.end(ctx, state.DrillAborted, log)
}

// RestoreForUp ends an active drill before lab up changes the lab; ok is
// false when none runs.
func (m *Manager) RestoreForUp(ctx context.Context, log io.Writer) (state.DrillRun, bool, error) {
	_, _, ok, err := m.Active()
	if err != nil || !ok {
		return state.DrillRun{}, false, err
	}
	r, err := m.end(ctx, state.DrillRestoredByUp, log)
	return r, true, err
}

// Expire restores the active drill if it is run (any run when run is "")
// and past its deadline. It reports whether it restored anything.
func (m *Manager) Expire(ctx context.Context, run string, log io.Writer) (bool, error) {
	r, _, ok, err := m.Active()
	if err != nil || !ok || (run != "" && r.Run != run) || m.now().Before(r.Deadline) {
		return false, err
	}
	_, err = m.end(ctx, state.DrillExpired, log)
	return true, err
}

func (m *Manager) end(ctx context.Context, outcome string, log io.Writer) (state.DrillRun, error) {
	r, d, err := m.active()
	if err != nil {
		return r, err
	}
	if r.Phase == state.DrillRunning {
		if c, err := m.CheckAlert(ctx); err == nil {
			r = c
		}
	}
	err = m.restore(ctx, d, &r, outcome, log)
	return r, err
}

// restore puts the saved objects back, runs restore.sh, checks the saved
// objects are as they were and moves the run to the history. On failure
// the run stays active in the restoring phase, with the error, so the
// next abort tries again.
func (m *Manager) restore(ctx context.Context, d Drill, r *state.DrillRun, outcome string, log io.Writer) error {
	if err := m.Store.Update(func(s *state.State) error {
		if s.ActiveDrill != nil && s.ActiveDrill.Run == r.Run {
			s.ActiveDrill.Phase = state.DrillRestoring
		}
		return nil
	}); err != nil {
		return err
	}
	fail := func(err error) error {
		_ = m.Store.Update(func(s *state.State) error {
			if s.ActiveDrill != nil && s.ActiveDrill.Run == r.Run {
				s.ActiveDrill.Error = err.Error()
			}
			return nil
		})
		return err
	}
	dyn, err := m.Kube()
	if err != nil {
		return fail(err)
	}
	logf(log, "putting back %d saved object(s)", len(r.Snapshot))
	if err := Restore(ctx, dyn, r.Snapshot); err != nil {
		return fail(err)
	}
	logf(log, "running restore.sh")
	out, err := m.script(ctx, d, *r, "restore", m.scriptTimeout())
	_, _ = io.WriteString(log, out)
	if err != nil {
		return fail(fmt.Errorf("restore.sh: %w", err))
	}
	diffs, err := Diff(ctx, dyn, r.Snapshot)
	if err != nil {
		return fail(err)
	}
	if len(diffs) > 0 {
		return fail(fmt.Errorf("not back as before the drill: %s", strings.Join(diffs, "; ")))
	}
	if m.Timer != nil {
		_ = m.Timer.Cancel(ctx, r.Timer)
	}
	now := m.now()
	return m.Store.Update(func(s *state.State) error {
		if s.ActiveDrill == nil || s.ActiveDrill.Run != r.Run {
			return nil // another lab finished it
		}
		s.EndDrill(outcome, now)
		*r = s.Drills[len(s.Drills)-1]
		return nil
	})
}

// Best is a drill's best times over its recovered runs.
type Best struct {
	Runs, Recovered      int
	Alert, Detect, Fixed time.Duration // zero: none yet
}

// BestTimes are the best times per drill from the history.
func BestTimes(history []state.DrillRun) map[string]Best {
	out := map[string]Best{}
	better := func(cur *time.Duration, d time.Duration) {
		if d > 0 && (*cur == 0 || d < *cur) {
			*cur = d
		}
	}
	for _, r := range history {
		b := out[r.Drill]
		b.Runs++
		if r.Outcome == state.DrillRecovered {
			b.Recovered++
			better(&b.Alert, r.TimeTo(r.AlertFired))
			better(&b.Detect, r.TimeTo(r.Acked))
			better(&b.Fixed, r.TimeTo(r.Recovered))
		}
		out[r.Drill] = b
	}
	return out
}

func logf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format+"\n", a...) }

func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}
