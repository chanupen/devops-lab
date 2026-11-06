package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/state"
)

// Runner deploys and removes single stacks. Providers implement it; the
// engine only decides order and records results. Everything a step prints
// (tool output, progress) goes to log, which the engine keeps in the ops log.
type Runner interface {
	Apply(ctx context.Context, stack string, log io.Writer) error
	Destroy(ctx context.Context, stack string, log io.Writer) error
}

// Releaser is implemented by runners whose stacks keep something outside
// the cluster (a secret in AWS Secrets Manager). When a stack goes with
// the cluster (Step.With), the engine calls Release instead of Destroy:
// the cluster's destroy removes everything inside it, and Release removes
// the rest. A runner without it releases nothing.
type Releaser interface {
	Release(ctx context.Context, stack string, log io.Writer) error
}

// Store is the state persistence the engine needs. *state.Store implements it.
type Store interface {
	Load() (state.State, error)
	Update(fn func(*state.State) error) error
	// OpenStepLog creates the log file for one step and returns its path.
	OpenStepLog(stack, action string, started time.Time) (io.WriteCloser, string, error)
}

// Phase marks where a step is in its run.
type Phase string

// Event phases.
const (
	Started  Phase = "started"
	Finished Phase = "finished"
	Failed   Phase = "failed"
)

// Event reports progress so callers (CLI output, TUI messages) can stream it.
type Event struct {
	Step  Step
	Phase Phase
	Err   error
	Log   string // path of the step's log file
}

// ErrNoRunner is returned by Execute when no provider is configured.
var ErrNoRunner = errors.New("no provider configured")

// Engine plans against stored state and executes plans through a Runner.
type Engine struct {
	graph  *Graph
	store  Store
	runner Runner
	now    func() time.Time
}

// New returns an engine. runner may be nil for planning only.
func New(g *Graph, store Store, runner Runner) *Engine {
	return &Engine{graph: g, store: store, runner: runner, now: time.Now}
}

// PlanUp loads state and plans bringing the lab to cfg.
func (e *Engine) PlanUp(cfg config.Config, opts PlanOptions) (Plan, error) {
	st, err := e.store.Load()
	if err != nil {
		return Plan{}, err
	}
	return PlanUp(e.graph, cfg, st, opts)
}

// PlanDown loads state and plans destroying deployed stacks.
func (e *Engine) PlanDown(cfg config.Config, opts DownOptions) (Plan, error) {
	st, err := e.store.Load()
	if err != nil {
		return Plan{}, err
	}
	return PlanDown(e.graph, cfg, st, opts)
}

// Execute runs the plan's steps in order and stops at the first failure.
// Each step is recorded in state and the ops log under command, the
// equivalent CLI command (for example "lab up"). events may be nil.
func (e *Engine) Execute(ctx context.Context, p Plan, command string, events func(Event)) error {
	if e.runner == nil {
		return ErrNoRunner
	}
	emit := func(ev Event) {
		if events != nil {
			events(ev)
		}
	}
	for _, step := range p.Steps {
		if !step.Action.Runs() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		started := e.now()
		w, path, err := e.store.OpenStepLog(step.Stack, string(step.Action), started)
		if err != nil {
			return fmt.Errorf("open log for %s %s: %w", step.Action, step.Stack, err)
		}
		emit(Event{Step: step, Phase: Started, Log: path})
		runErr := e.runStep(ctx, step, command, w)
		_ = w.Close() // the log is best effort; it never fails a step
		if err := e.record(p, step, command, started, path, runErr); err != nil {
			return errors.Join(runErr, fmt.Errorf("record %s %s: %w", step.Action, step.Stack, err))
		}
		if runErr != nil {
			emit(Event{Step: step, Phase: Failed, Err: runErr, Log: path})
			return fmt.Errorf("%s %s: %w (log: %s)", step.Action, step.Stack, runErr, path)
		}
		emit(Event{Step: step, Phase: Finished, Log: path})
	}
	return nil
}

// runStep calls the runner with a timestamped log and writes a header and
// a result line around its output.
func (e *Engine) runStep(ctx context.Context, step Step, command string, w io.Writer) error {
	tw := newTimestampWriter(w, e.now)
	start := e.now()
	_, _ = fmt.Fprintf(tw, "%s: %s %s\n", command, step.Action, step.Stack)
	var err error
	rel, releases := e.runner.(Releaser)
	switch {
	case step.With != "" && !releases:
		_, _ = fmt.Fprintf(tw, "nothing to run: removed with the %s\n", step.With)
		_ = tw.Flush()
		return nil
	case step.With != "":
		_, _ = fmt.Fprintf(tw, "removed with the %s; releasing what it keeps outside it\n", step.With)
		err = rel.Release(ctx, step.Stack, tw)
	case step.Action == Apply:
		err = e.runner.Apply(ctx, step.Stack, tw)
	default:
		err = e.runner.Destroy(ctx, step.Stack, tw)
	}
	d := e.now().Sub(start).Round(100 * time.Millisecond)
	if err != nil {
		_, _ = fmt.Fprintf(tw, "failed after %s: %v\n", d, err)
	} else {
		_, _ = fmt.Fprintf(tw, "ok in %s\n", d)
	}
	_ = tw.Flush()
	return err
}

// record writes the step's outcome to the stack state and the ops log.
func (e *Engine) record(p Plan, step Step, command string, started time.Time, log string, runErr error) error {
	var dropped []state.Op
	err := e.store.Update(func(s *state.State) error {
		now := e.now()
		op := state.Op{
			Command: command, Stack: step.Stack, Action: string(step.Action),
			Status: "ok", Started: started, Finished: now, Log: log,
		}
		ss := state.StackState{Status: state.Applied, UpdatedAt: now}
		if runErr != nil {
			op.Status, op.Error = "failed", runErr.Error()
			// A failed destroy may leave resources behind, so it stays Failed too.
			ss.Status, ss.Error = state.Failed, runErr.Error()
		}
		dropped = s.AppendOp(op)
		if step.Action == Destroy && runErr == nil {
			delete(s.Stacks, step.Stack)
		} else {
			s.Stacks[step.Stack] = ss
		}
		if step.Stack == RevisionStack && runErr == nil {
			if step.Action == Apply {
				s.Revision = p.Revision
			} else {
				s.Revision = ""
			}
		}
		if len(s.Stacks) == 0 {
			s.Mode = ""
		} else if s.Mode == "" {
			s.Mode = p.Mode
		}
		return nil
	})
	if r, ok := e.store.(interface{ RemoveLogs([]state.Op) }); ok && err == nil {
		r.RemoveLogs(dropped)
	}
	return err
}
