package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/state"
)

// Request is one operation the TUI asks for. It maps one to one to a CLI
// command, which Command returns.
type Request struct {
	Down   bool     // lab down; otherwise lab up
	Stacks []string // empty: the whole profile (lab up only)
}

// Command is the equivalent CLI command, as the ops log records it.
func (r Request) Command() string {
	c := "lab up"
	if r.Down {
		c = "lab down"
	}
	if len(r.Stacks) > 0 {
		c += " --stacks " + strings.Join(r.Stacks, ",")
	}
	return c
}

// Progress is one update from a running operation: an engine event or a
// line of output (the plan, warnings, step logs).
type Progress struct {
	Event *engine.Event
	Line  string
}

// Actions runs operations through the same engine and providers as the CLI.
// cmd/lab implements it; tests pass a fake.
type Actions interface {
	// Plan returns what Run would do for req, without changing anything.
	Plan(ctx context.Context, req Request) (engine.Plan, error)
	// Run plans req and executes it, reporting each line and event to
	// progress, and returns the plan it ran. The TUI runs one at a time.
	Run(ctx context.Context, req Request, progress func(Progress)) (engine.Plan, error)
	// Report runs a read-only CLI command (status, doctor, plan) and returns
	// what it prints.
	Report(ctx context.Context, name string) (string, error)
}

// opState is the operation in progress. Only Update touches it.
type opState struct {
	req     Request
	started time.Time
	step    string // e.g. "obs: applying"
	stepLog string // log file of the current step
	last    string // last output line
	lines   *ring  // the whole operation's output, for the live log view
	cancel  context.CancelFunc
}

// Messages from a running operation.
type (
	opProgressMsg struct{ batch []Progress }
	opDoneMsg     struct {
		req  Request
		plan engine.Plan
		err  error
		took time.Duration
	}
	planMsg struct {
		req  Request
		plan engine.Plan
		err  error
	}
	reportMsg struct {
		name string
		out  string
		err  error
	}
)

// confirmState is a pending yes/no question before a destructive request.
type confirmState struct {
	req      Request
	question string
}

// startOp runs req in the background. Progress reaches Update in batches at
// most redrawEvery apart, so a chatty step cannot flood the redraw loop.
func (m Model) startOp(req Request) (Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.op = &opState{req: req, started: m.now(), lines: newRing(logLines), cancel: cancel}
	m.op.lines.add("$ " + req.Command())
	m.flash = ""
	acts, out, every := m.opts.Actions, m.opEvents, redrawEvery
	run := func() tea.Msg {
		var (
			mu      sync.Mutex
			pending []Progress
			done    = make(chan struct{})
			flushed = make(chan struct{})
		)
		flush := func() {
			mu.Lock()
			batch := pending
			pending = nil
			mu.Unlock()
			if len(batch) > 0 {
				out <- opProgressMsg{batch: batch}
			}
		}
		go func() {
			defer close(flushed)
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					flush()
				case <-done:
					flush()
					return
				}
			}
		}()
		start := time.Now()
		plan, err := acts.Run(ctx, req, func(p Progress) {
			mu.Lock()
			pending = append(pending, p)
			mu.Unlock()
		})
		close(done)
		<-flushed
		out <- opDoneMsg{req: req, plan: plan, err: err, took: time.Since(start)}
		return nil
	}
	return m, tea.Batch(run, m.listenOp())
}

func (m Model) listenOp() tea.Cmd {
	ch := m.opEvents
	return func() tea.Msg { return <-ch }
}

// applyProgress folds a batch into the operation state.
func (m Model) applyProgress(batch []Progress) Model {
	if m.op == nil {
		return m
	}
	for _, p := range batch {
		if p.Event != nil {
			e := *p.Event
			verb := map[engine.Action][3]string{
				engine.Apply:   {"applying", "applied", "apply failed"},
				engine.Destroy: {"destroying", "destroyed", "destroy failed"},
			}[e.Step.Action]
			switch e.Phase {
			case engine.Started:
				m.op.step, m.op.stepLog = e.Step.Stack+": "+verb[0], e.Log
			case engine.Finished:
				m.op.step = e.Step.Stack + ": " + verb[1]
			case engine.Failed:
				m.op.step = e.Step.Stack + ": " + verb[2]
			}
			continue
		}
		m.op.last = p.Line
		m.op.lines.add(p.Line)
	}
	return m // a live view shares op.lines and keeps its place
}

// finishOp clears the operation and says how it ended in the ops strip.
func (m Model) finishOp(msg opDoneMsg) (Model, tea.Cmd) {
	if m.op != nil {
		m.op.cancel()
	}
	m.op = nil
	cmd := msg.req.Command()
	var busy *state.BusyError
	switch {
	case errors.As(msg.err, &busy):
		m.flash = statusFailed.symbol() + " " + busy.Error() + "; " + msg.req.Command() + " did not run"
	case msg.err != nil:
		m.flash = statusFailed.symbol() + " " + cmd + " failed: " + firstLine(msg.err.Error()) + "  (5 for the ops log)"
	case msg.plan.Changes() == 0:
		m.flash = statusHealthy.symbol() + " " + cmd + ": nothing to do"
		if n := msg.plan.Count(engine.Skip); n > 0 {
			m.flash += fmt.Sprintf(" (%d skipped, not supported yet)", n)
		}
	default:
		m.flash = fmt.Sprintf("%s %s: done in %s", statusHealthy.symbol(), cmd, msg.took.Round(100*time.Millisecond))
		if n := msg.plan.Count(engine.Skip); n > 0 {
			m.flash += fmt.Sprintf(", %d skipped (not supported yet)", n)
		}
	}
	if m.view != nil && m.view.live {
		m.view.live = false
	}
	return m, m.loadState()
}

// request starts req, or says why it cannot: no actions wired, or another
// operation in this TUI. Another process holding the lab is reported when
// Run returns.
func (m Model) request(req Request) (Model, tea.Cmd) {
	switch {
	case m.d.readOnly != "":
		m.flash = m.readOnlyFlash(req)
		return m, nil
	case m.opts.Actions == nil:
		m.flash = "actions are not available; run: " + req.Command()
		return m, nil
	case m.op != nil:
		m.flash = statusProgressing.symbol() + " busy: " + m.op.req.Command() + " is running"
		return m, nil
	}
	return m.startOp(req)
}

// askDown plans req in the background, then asks before destroying.
func (m Model) askDown(req Request) (Model, tea.Cmd) {
	if m.d.readOnly != "" {
		m.flash = m.readOnlyFlash(req)
		return m, nil
	}
	if m.op != nil {
		return m.request(req) // reports busy
	}
	if m.opts.Actions == nil {
		m.flash = "actions are not available; run: " + req.Command()
		return m, nil
	}
	acts := m.opts.Actions
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		p, err := acts.Plan(ctx, req)
		return planMsg{req: req, plan: p, err: err}
	}
}

// confirmFor turns a down plan into a question, or a flash if there is
// nothing to destroy.
func (m Model) confirmFor(msg planMsg) Model {
	if msg.err != nil {
		m.flash = statusFailed.symbol() + " " + msg.req.Command() + ": " + firstLine(msg.err.Error())
		return m
	}
	var stacks []string
	for _, s := range msg.plan.Steps {
		if s.Action == engine.Destroy {
			stacks = append(stacks, s.Stack)
		}
	}
	if len(stacks) == 0 {
		m.flash = "nothing to destroy: " + strings.Join(msg.req.Stacks, ", ") + " is not deployed"
		return m
	}
	q := "Destroy " + strings.Join(stacks, ", ") + "?"
	if len(stacks) > len(msg.req.Stacks) {
		q = fmt.Sprintf("Destroy %s and %d %s that %s it (%s)?", strings.Join(msg.req.Stacks, ", "),
			len(stacks)-len(msg.req.Stacks), plural(len(stacks)-len(msg.req.Stacks), "stack", "stacks"),
			plural(len(stacks)-len(msg.req.Stacks), "needs", "need"), strings.Join(stacks[:len(stacks)-len(msg.req.Stacks)], ", "))
	}
	m.confirm = &confirmState{req: msg.req, question: q}
	return m
}

// runReport runs a read-only command in the background and opens its output.
func (m Model) runReport(name string) (Model, tea.Cmd) {
	if m.opts.Actions == nil {
		m.flash = "reports are not available; run: lab " + name
		return m, nil
	}
	m.flash = statusProgressing.symbol() + " $ lab " + name
	acts := m.opts.Actions
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		out, err := acts.Report(ctx, name)
		return reportMsg{name: name, out: out, err: err}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// readOnlyFlash says why an action did nothing in a read-only TUI.
func (m Model) readOnlyFlash(req Request) string {
	return statusOff.symbol() + " read-only: another lab is running: " + m.d.readOnly + "; " + req.Command() + " did not run"
}
