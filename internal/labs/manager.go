package labs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	if !slices.Contains(l.Modes, m.Mode) {
		return l, fmt.Errorf("lab %s runs in %s mode, not %s", l.ID, strings.Join(l.Modes, ", "), m.Mode)
	}
	if missing := m.missingTools(l); len(missing) > 0 {
		return l, fmt.Errorf("lab %s needs %s on PATH: the lab README says how to install them", l.ID, strings.Join(missing, ", "))
	}
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
		if err := m.Clusters.Create(ctx, ClusterName(l), m.Kubeconfig(l), log); err != nil {
			return l, fmt.Errorf("scratch cluster %s: %w", ClusterName(l), err)
		}
	}
	now := m.now()
	return l, m.Store.Update(func(s *state.State) error {
		if s.Labs == nil {
			s.Labs = map[string]state.LabProgress{}
		}
		p := s.Labs[l.ID]
		if p.Step == 0 {
			p.Step, p.Started = 1, now
		}
		if p.Session.IsZero() {
			p.Session = now
		}
		s.Labs[l.ID], s.ActiveLab = p, l.ID
		return nil
	})
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

// Stop ends the active lab's session and deletes its scratch cluster.
// Progress is kept: starting the lab again picks up at the same step.
func (m *Manager) Stop(ctx context.Context, log io.Writer) (Lab, error) {
	l, _, err := m.active()
	if err != nil {
		return l, err
	}
	if l.Needs == NeedsScratch && m.Clusters != nil {
		if err := m.Clusters.Delete(ctx, ClusterName(l), m.Kubeconfig(l), log); err != nil {
			return l, fmt.Errorf("scratch cluster %s: %w", ClusterName(l), err)
		}
	}
	now := m.now()
	return l, m.Store.Update(func(s *state.State) error {
		p := s.Labs[l.ID]
		p.Spent, p.Session = p.TimeSpent(now), time.Time{}
		s.Labs[l.ID], s.ActiveLab = p, ""
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
	out, err := m.Runner.Run(ctx, l.ResetScript(), l.Dir, scriptEnv(l, 0, m.Kubeconfig(l)))
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
	res := verify(ctx, m.Runner, l, n, m.Kubeconfig(l), timeout)
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
