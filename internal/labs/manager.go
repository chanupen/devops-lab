package labs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/chanupen/devops-lab/internal/state"
)

// ScratchPrefix names the cluster of a needs: scratch lab: lab-scratch-<id>.
const ScratchPrefix = "lab-scratch-"

// ResetTimeout is how long reset.sh may run.
const ResetTimeout = 5 * time.Minute

// Clusters makes and removes the single-node clusters scratch labs run on.
// internal/provider/local implements it with kind on rootless Podman.
type Clusters interface {
	Exists(name string) (bool, error)
	// Create makes the cluster if it is missing and writes its kubeconfig.
	Create(ctx context.Context, name, kubeconfig string, log io.Writer) error
	Delete(ctx context.Context, name, kubeconfig string, log io.Writer) error
	// Uptime says whether the cluster's node runs, and since when.
	Uptime(name string) (Uptime, error)
}

// Uptime is when a scratch cluster's node last started and stopped.
type Uptime struct {
	Exists  bool
	Running bool
	Started time.Time
	Stopped time.Time // the last stop; zero if it never stopped
}

// TimeSpent is p's time: the sessions before this one, and the open session
// only while the lab's cluster ran. A node that stopped (a reboot) or is gone
// adds nothing after that; once it starts again, time counts from then. up
// nil means the uptime is unknown (a lab on the main cluster) and the whole
// session counts.
func TimeSpent(p state.LabProgress, up *Uptime, now time.Time) time.Duration {
	if p.Session.IsZero() || up == nil {
		return p.TimeSpent(now)
	}
	if !up.Exists {
		return p.Spent
	}
	from, to := p.Session, now
	if up.Started.After(from) {
		from = up.Started
	}
	if !up.Running {
		to = up.Stopped
	}
	if !to.After(from) {
		return p.Spent
	}
	return p.Spent + to.Sub(from)
}

// TimeSpent is lab l's time spent, asking the cluster for its uptime.
// With no cluster to ask (Podman unreachable) a scratch lab's open session
// adds nothing: its cluster cannot be running.
func (m *Manager) TimeSpent(l Lab, p state.LabProgress) time.Duration {
	if l.Needs != NeedsScratch || p.Session.IsZero() {
		return TimeSpent(p, nil, m.now())
	}
	up := &Uptime{}
	if m.Clusters != nil {
		if u, err := m.Clusters.Uptime(m.Scratch(l)); err == nil {
			up = &u
		}
	}
	return TimeSpent(p, up, m.now())
}

// Manager starts, verifies and stops labs, and keeps their progress in
// the state file.
type Manager struct {
	Labs  []Lab
	Store *state.Store
	// Mode is the lab mode labs run in; a lab must list it in modes.
	Mode     string
	Clusters Clusters // nil: scratch labs cannot start
	Runner   Runner
	// Home is ~/.lab: a scratch lab's kubeconfig is Home/labs/<id>/kubeconfig.
	Home string
	// MainKubeconfig is the main lab's cluster, for needs: lab.
	MainKubeconfig string
	LookPath       func(string) (string, error)
	Now            func() time.Time
	// Timeout is how long a verify script may run; 0 is VerifyTimeout.
	Timeout time.Duration
	// Env is added to every script's environment, after lab's own
	// variables (the e2e test gives the scripts a throwaway HOME this way).
	Env []string
	// EnvFunc adds to Env each time a script runs, so values that expire
	// (cloud mode: the session's temporary keys) are fresh. Nil adds nothing.
	EnvFunc func() ([]string, error)
	// Prepare readies the host for a lab before it is recorded as started
	// (local mode: the mirror files that let the cluster pull from the lab
	// registry). Nil does nothing.
	Prepare func(Lab) error
	// ScratchName names a scratch lab's cluster; nil is ClusterName. The
	// e2e test uses its own names, so it never meets a learner's cluster.
	ScratchName func(Lab) string
	// Platform is "linux/amd64" and the like, for the install commands;
	// "" is this host's.
	Platform string
}

// env is Env plus EnvFunc's values.
func (m *Manager) env() ([]string, error) {
	env := slices.Clone(m.Env)
	if m.EnvFunc == nil {
		return env, nil
	}
	more, err := m.EnvFunc()
	if err != nil {
		return nil, fmt.Errorf("the lab scripts' environment: %w", err)
	}
	return append(env, more...), nil
}

// ScriptEnv is what every lab script of this manager runs with besides
// lab's own variables: Env and EnvFunc's values. The e2e test runs the
// solutions with it.
func (m *Manager) ScriptEnv() ([]string, error) { return m.env() }

// Scratch is the name of lab l's scratch cluster.
func (m *Manager) Scratch(l Lab) string {
	if m.ScratchName != nil {
		return m.ScratchName(l)
	}
	return ClusterName(l)
}

func (m *Manager) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}

// Find returns the lab with this ID, or whose number is n ("01" or "1").
func (m *Manager) Find(id string) (Lab, error) {
	for _, l := range m.Labs {
		if l.ID == id || l.Number() == id || strings.TrimLeft(l.Number(), "0") == id {
			return l, nil
		}
	}
	return Lab{}, fmt.Errorf("no lab %q: lab labs list shows them", id)
}

// ClusterName is the scratch cluster of lab l.
func ClusterName(l Lab) string { return ScratchPrefix + l.ID }

// Kubeconfig is the cluster lab l's steps run against.
func (m *Manager) Kubeconfig(l Lab) string {
	if l.Needs == NeedsLab {
		return m.MainKubeconfig
	}
	return filepath.Join(m.Home, "labs", l.ID, "kubeconfig")
}

// Active returns the started lab and its progress; ok is false when none is.
func (m *Manager) Active() (Lab, state.LabProgress, bool, error) {
	st, err := m.Store.Load()
	if err != nil || st.ActiveLab == "" {
		return Lab{}, state.LabProgress{}, false, err
	}
	l, err := m.Find(st.ActiveLab)
	if err != nil {
		return Lab{}, state.LabProgress{}, false, fmt.Errorf("the started lab %s is no longer in labs/: lab labs stop", st.ActiveLab)
	}
	return l, st.Labs[l.ID], true, nil
}

// active is Active, as an error when no lab is started.
func (m *Manager) active() (Lab, state.LabProgress, error) {
	l, p, ok, err := m.Active()
	if err == nil && !ok {
		err = errors.New("no lab is started: lab labs start <id>")
	}
	return l, p, err
}

// Start makes lab id's cluster ready and makes it the active lab. Starting
// the active lab again picks up where it was.
func (m *Manager) Start(ctx context.Context, id string, log io.Writer) (Lab, error) {
	l, err := m.Find(id)
	if err != nil {
		return l, err
	}
	st, err := m.Store.Load()
	if err != nil {
		return l, err
	}
	if st.ActiveLab != "" && st.ActiveLab != l.ID {
		return l, fmt.Errorf("lab %s is started; lab labs stop first (one lab at a time)", st.ActiveLab)
	}
	if st.ActiveDrill != nil {
		return l, errors.New("a drill is running on the main lab; finish it first: lab drills verify, or lab drills abort")
	}
	if !slices.Contains(l.Modes, m.Mode) {
		return l, fmt.Errorf("lab %s runs in %s mode, not %s", l.ID, strings.Join(l.Modes, ", "), m.Mode)
	}
	if missing := m.missingTools(l); len(missing) > 0 {
		return l, m.toolsError(l, missing)
	}
	// Before the cluster is touched: a node that was stopped (a reboot)
	// counts only up to when it stopped.
	spent := m.TimeSpent(l, st.Labs[l.ID])
	switch l.Needs {
	case NeedsLab:
		var down []string
		for _, s := range l.Stacks {
			if !st.IsApplied(s) {
				down = append(down, s)
			}
		}
		if len(down) > 0 {
			return l, fmt.Errorf("lab %s runs on the main lab and needs %s: lab up --stacks %s", l.ID, strings.Join(down, ", "), strings.Join(down, ","))
		}
	case NeedsScratch:
		if m.Clusters == nil {
			return l, errors.New("scratch clusters are not available in this mode")
		}
		name := m.Scratch(l)
		// A cluster with this name that this state did not start belongs to
		// someone else: another lab home, or a test run. Never reuse it.
		exists, err := m.Clusters.Exists(name)
		if err != nil {
			return l, fmt.Errorf("scratch cluster %s: %w", name, err)
		}
		if exists && st.ActiveLab != l.ID {
			return l, fmt.Errorf("a kind cluster named %s already exists, but %s did not start it "+
				"(another lab home or a test run may own it); lab labs start never reuses it. "+
				"If it is yours and unused, delete it: KIND_EXPERIMENTAL_PROVIDER=podman kind delete cluster --name %s",
				name, m.Store.Path(), name)
		}
		if err := m.Clusters.Create(ctx, name, m.Kubeconfig(l), log); err != nil {
			return l, m.undoCreate(ctx, name, l, exists, fmt.Errorf("scratch cluster %s: %w", name, err), log)
		}
		// The lab is started only once its cluster is there.
		ok, err := m.Clusters.Exists(name)
		if err != nil {
			return l, m.undoCreate(ctx, name, l, exists, fmt.Errorf("scratch cluster %s: checking it after create: %w", name, err), log)
		}
		if !ok {
			return l, m.undoCreate(ctx, name, l, exists, fmt.Errorf("scratch cluster %s is not there after create", name), log)
		}
	}
	if m.Prepare != nil {
		if err := m.Prepare(l); err != nil {
			return l, fmt.Errorf("lab %s: %w", l.ID, err)
		}
	}
	// The scripts' environment must resolve now (cloud: an AWS session and
	// a running cloud lab), not at the first verify.
	if _, err := m.env(); err != nil {
		return l, fmt.Errorf("lab %s: %w", l.ID, err)
	}
	// Only now, with the cluster up and every check passed, is the lab
	// recorded as started: a start that fails leaves no progress behind.
	now := m.now()
	return l, m.Store.Update(func(s *state.State) error {
		if s.Labs == nil {
			s.Labs = map[string]state.LabProgress{}
		}
		p := s.Labs[l.ID]
		if p.Step == 0 {
			p.Step, p.Started = 1, now
		}
		// The session so far is folded into Spent; a new one counts from now.
		p.Spent, p.Session = spent, now
		s.Labs[l.ID], s.ActiveLab = p, l.ID
		return nil
	})
}

// undoCreate deletes a cluster this start created before it failed, so a
// failed start leaves neither progress nor a cluster that the next start
// would refuse as foreign. A cluster that was there before is left alone.
func (m *Manager) undoCreate(ctx context.Context, name string, l Lab, existed bool, err error, log io.Writer) error {
	if existed {
		return err
	}
	if derr := m.Clusters.Delete(ctx, name, m.Kubeconfig(l), log); derr != nil {
		return fmt.Errorf("%w; removing the half-made cluster also failed: %w", err, derr)
	}
	return err
}

func (m *Manager) missingTools(l Lab) []string {
	look := m.LookPath
	if look == nil {
		return nil
	}
	var missing []string
	for _, t := range l.Tools {
		if _, err := look(t); err != nil {
			missing = append(missing, t)
		}
	}
	return missing
}

// toolsError says which tools are missing and, for each, the pinned
// install command lab.yaml gives for this platform.
func (m *Manager) toolsError(l Lab, missing []string) error {
	platform := m.Platform
	if platform == "" {
		platform = runtime.GOOS + "/" + runtime.GOARCH
	}
	var b strings.Builder
	fmt.Fprintf(&b, "lab %s needs %s on PATH", l.ID, strings.Join(missing, ", "))
	for _, t := range missing {
		if c := l.Install[t][platform]; c != "" {
			fmt.Fprintf(&b, "\n  install %s:\n    %s", t, c)
		} else {
			fmt.Fprintf(&b, "\n  %s: install it with your package manager (lab doctor lists the tool versions lab expects)", t)
		}
	}
	return errors.New(b.String())
}

// Stop ends the active lab's session and deletes its scratch cluster. It
// returns the progress the lab reached, with its time spent. A scratch
// lab's progress is then cleared, steps and time: the cluster that held
// the work is gone, so the next start begins at step 1. A lab on the main
// cluster keeps its steps and starts its time from zero.
func (m *Manager) Stop(ctx context.Context, log io.Writer) (Lab, state.LabProgress, error) {
	l, p, err := m.active()
	if err != nil {
		return l, p, err
	}
	p.Spent, p.Session = m.TimeSpent(l, p), time.Time{}
	l, err = m.stop(ctx, l, log)
	return l, p, err
}

func (m *Manager) stop(ctx context.Context, l Lab, log io.Writer) (Lab, error) {
	if l.Needs == NeedsScratch && m.Clusters != nil {
		name := m.Scratch(l)
		exists, err := m.Clusters.Exists(name)
		switch {
		case err != nil:
			return l, fmt.Errorf("scratch cluster %s: %w", name, err)
		case !exists:
			// Already gone (deleted by hand, or never there): the lab's
			// state still has to be cleared.
			_, _ = fmt.Fprintf(log, "scratch cluster %s was not found; clearing the lab's state\n", name)
			if err := os.Remove(m.Kubeconfig(l)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return l, err
			}
		default:
			if err := m.Clusters.Delete(ctx, name, m.Kubeconfig(l), log); err != nil {
				return l, fmt.Errorf("scratch cluster %s: %w", name, err)
			}
		}
	}
	return l, m.Store.Update(func(s *state.State) error {
		s.ActiveLab = ""
		if l.Needs == NeedsScratch {
			delete(s.Labs, l.ID)
			return nil
		}
		p := s.Labs[l.ID]
		p.Spent, p.Session = 0, time.Time{}
		s.Labs[l.ID] = p
		return nil
	})
}

// Reset runs the active lab's reset.sh and starts its steps over. Time
// spent and attempts are kept.
func (m *Manager) Reset(ctx context.Context, log io.Writer) (Lab, error) {
	l, _, err := m.active()
	if err != nil {
		return l, err
	}
	ctx, cancel := context.WithTimeout(ctx, ResetTimeout)
	defer cancel()
	env, err := m.env()
	if err != nil {
		return l, err
	}
	out, err := m.Runner.Run(ctx, l.ResetScript(), l.Dir, append(scriptEnv(l, 0, m.Kubeconfig(l)), env...))
	_, _ = io.WriteString(log, out)
	if err != nil {
		return l, fmt.Errorf("reset.sh: %w", err)
	}
	return l, m.Store.Update(func(s *state.State) error {
		p := s.Labs[l.ID]
		p.Step, p.Hints, p.Completed = 1, nil, time.Time{}
		s.Labs[l.ID] = p
		return nil
	})
}

// stepOrCurrent is n, or the current step when n is 0 (the last step once
// the lab is done).
func stepOrCurrent(l Lab, p state.LabProgress, n int) (int, error) {
	if n == 0 {
		n = min(max(p.Step, 1), len(l.Steps))
	}
	if _, ok := l.Step(n); !ok {
		return 0, fmt.Errorf("lab %s has steps 1 to %d", l.ID, len(l.Steps))
	}
	return n, nil
}

// Verify runs step n's verify script (0: the current step) against the
// active lab's cluster. Passing the current step moves the lab on.
func (m *Manager) Verify(ctx context.Context, n int) (Lab, Result, error) {
	l, p, err := m.active()
	if err != nil {
		return l, Result{}, err
	}
	if n, err = stepOrCurrent(l, p, n); err != nil {
		return l, Result{}, err
	}
	timeout := m.Timeout
	if timeout == 0 {
		timeout = VerifyTimeout
	}
	env, err := m.env()
	if err != nil {
		return l, Result{}, err
	}
	res := verify(ctx, m.Runner, l, n, m.Kubeconfig(l), timeout, env)
	now := m.now()
	return l, res, m.Store.Update(func(s *state.State) error {
		p := s.Labs[l.ID]
		if p.Attempts == nil {
			p.Attempts = map[int]int{}
		}
		p.Attempts[n]++
		if res.Passed && n == p.Step {
			p.Step++
			if p.Step > len(l.Steps) && p.Completed.IsZero() {
				p.Completed = now
			}
		}
		s.Labs[l.ID] = p
		return nil
	})
}

// Hint returns the next hint of step n (0: the current step) of the
// active lab, and which one it is. Once all are shown it says so.
func (m *Manager) Hint(n int) (l Lab, step int, hint string, err error) {
	l, p, err := m.active()
	if err != nil {
		return l, 0, "", err
	}
	if n, err = stepOrCurrent(l, p, n); err != nil {
		return l, 0, "", err
	}
	s, _ := l.Step(n)
	shown := p.Hints[n]
	if shown >= len(s.Hints) {
		return l, n, fmt.Sprintf("No more hints for step %d. lab labs solution %d shows the answer.", n, n), nil
	}
	hint = fmt.Sprintf("Hint %d of %d: %s", shown+1, len(s.Hints), s.Hints[shown])
	return l, n, hint, m.Store.Update(func(st *state.State) error {
		p := st.Labs[l.ID]
		if p.Hints == nil {
			p.Hints = map[int]int{}
		}
		p.Hints[n] = shown + 1
		st.Labs[l.ID] = p
		return nil
	})
}

// Solution returns step n's reference solution of lab id (the active lab
// when id is empty) and records that it was shown.
func (m *Manager) Solution(id string, n int) (Lab, string, error) {
	var l Lab
	var err error
	if id == "" {
		l, _, err = m.active()
	} else {
		l, err = m.Find(id)
	}
	if err != nil {
		return l, "", err
	}
	if _, ok := l.Step(n); !ok {
		return l, "", fmt.Errorf("lab %s has steps 1 to %d", l.ID, len(l.Steps))
	}
	b, err := os.ReadFile(l.SolveScript(n))
	if err != nil {
		return l, "", err
	}
	text := string(b)
	// Files the script applies from solution/ (named NN-*), shown after it.
	extra, _ := filepath.Glob(filepath.Join(l.Dir, "solution", fmt.Sprintf("%02d-*", n)))
	for _, f := range extra {
		if f == l.SolveScript(n) {
			continue
		}
		if b, err := os.ReadFile(f); err == nil {
			text += fmt.Sprintf("\n--- solution/%s ---\n%s", filepath.Base(f), b)
		}
	}
	return l, text, m.Store.Update(func(st *state.State) error {
		if st.Labs == nil {
			st.Labs = map[string]state.LabProgress{}
		}
		p := st.Labs[l.ID]
		if !slices.Contains(p.Solutions, n) {
			p.Solutions = append(p.Solutions, n)
			slices.Sort(p.Solutions)
		}
		st.Labs[l.ID] = p
		return nil
	})
}

// Mark is a step's state as the CLI and the TUI show it.
type Mark int

// Marks, with the symbols the rest of lab uses.
const (
	NotStarted Mark = iota // ○
	Current                // ◐
	Done                   // ●
)

// Symbol is ●, ◐ or ○.
func (k Mark) Symbol() string { return [...]string{"○", "◐", "●"}[k] }

// StepMark is step n's mark for progress p.
func StepMark(p state.LabProgress, n int) Mark {
	switch {
	case p.Step == 0 || n > p.Step:
		return NotStarted
	case n < p.Step:
		return Done
	}
	return Current
}

// LabMark is the whole lab's mark: done, in progress or not started.
func LabMark(l Lab, p state.LabProgress) Mark {
	switch {
	case p.Step == 0:
		return NotStarted
	case p.Step > len(l.Steps):
		return Done
	}
	return Current
}

// DoneSteps is how many steps of l progress p has done.
func DoneSteps(l Lab, p state.LabProgress) int { return min(max(p.Step-1, 0), len(l.Steps)) }
