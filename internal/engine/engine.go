package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/state"
)

// Runner deploys and removes single stacks. Providers implement it; the
// engine only decides order and records results.
type Runner interface {
	Apply(ctx context.Context, stack string) error
	Destroy(ctx context.Context, stack string) error
}

// Store is the state persistence the engine needs. *state.Store implements it.
type Store interface {
	Load() (state.State, error)
	Update(fn func(*state.State) error) error
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

// PlanDown loads state and plans destroying everything deployed.
func (e *Engine) PlanDown(cfg config.Config) (Plan, error) {
	st, err := e.store.Load()
	if err != nil {
		return Plan{}, err
	}
	return PlanDown(e.graph, cfg, st)
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
		if step.Action == Keep {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		emit(Event{Step: step, Phase: Started})
		started := e.now()
		var runErr error
		if step.Action == Apply {
			runErr = e.runner.Apply(ctx, step.Stack)
		} else {
			runErr = e.runner.Destroy(ctx, step.Stack)
		}
		if err := e.record(p, step, command, started, runErr); err != nil {
			return errors.Join(runErr, fmt.Errorf("record %s %s: %w", step.Action, step.Stack, err))
		}
		if runErr != nil {
			emit(Event{Step: step, Phase: Failed, Err: runErr})
			return fmt.Errorf("%s %s: %w", step.Action, step.Stack, runErr)
		}
		emit(Event{Step: step, Phase: Finished})
	}
	return nil
}

// record writes the step's outcome to the stack state and the ops log.
func (e *Engine) record(p Plan, step Step, command string, started time.Time, runErr error) error {
	return e.store.Update(func(s *state.State) error {
		now := e.now()
		op := state.Op{
			Command: command, Stack: step.Stack, Action: string(step.Action),
			Status: "ok", Started: started, Finished: now,
		}
		ss := state.StackState{Status: state.Applied, UpdatedAt: now}
		if runErr != nil {
			op.Status, op.Error = "failed", runErr.Error()
			// A failed destroy may leave resources behind, so it stays Failed too.
			ss.Status, ss.Error = state.Failed, runErr.Error()
		}
		s.AppendOp(op)
		if step.Action == Destroy && runErr == nil {
			delete(s.Stacks, step.Stack)
		} else {
			s.Stacks[step.Stack] = ss
		}
		if len(s.Stacks) == 0 {
			s.Mode = ""
		} else if s.Mode == "" {
			s.Mode = p.Mode
		}
		return nil
	})
}
