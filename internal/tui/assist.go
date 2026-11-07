package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/chanupen/devops-lab/internal/assist"
	"github.com/chanupen/devops-lab/internal/state"
)

// AssistRequest selects live evidence, never a drill's answer or solution.
type AssistRequest struct {
	Kind, Name string
	Labels     map[string]string
}

// Command is the equivalent CLI invocation, with exact alert selectors.
func (r AssistRequest) Command(flags ...string) string {
	args := []string{"lab", "assist", r.Kind, r.Name, "--rules-first"}
	keys := make([]string, 0, len(r.Labels))
	for k := range r.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--label", k+"="+r.Labels[k])
	}
	args = append(args, flags...)
	for i, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\n\r'\"\\$`;|&<>()*?![]{}") {
			args[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
		}
	}
	return strings.Join(args, " ")
}

// Assistant uses the CLI's runner and restricted identities. Both calls run
// off the key loop; Run must honor cancellation and the runner's deadline.
type Assistant interface {
	Alerts(context.Context) ([]assist.Alert, error)
	Run(context.Context, AssistRequest, func(assist.Progress)) (assist.Note, error)
}

type assistState int

const (
	assistIdle assistState = iota
	assistRunning
	assistDone
	assistAbstained
	assistError
	assistCancelled
)

func (s assistState) String() string {
	return [...]string{"idle", "running", "done", "abstained", "error", "cancelled"}[s]
}

type assistPane struct {
	open    bool
	state   assistState
	id      int
	picking bool
	alerts  []assist.Alert
	cursor  int
	command string
	stage   string
	note    assist.Note
	text    *ring
	view    viewer
	cancel  context.CancelFunc
	ctx     context.Context
	events  chan tea.Msg
}

type assistResult struct {
	note assist.Note
	err  error
}

type assistMsg struct {
	id     int
	batch  []assist.Progress
	result *assistResult
}

type assistAlertsMsg struct {
	id     int
	alerts []assist.Alert
	err    error
}

func (m Model) openAssistant() (tea.Model, tea.Cmd) {
	if m.screen == screenDrills {
		run := m.d.st.ActiveDrill
		if run == nil || run.Phase != state.DrillRunning {
			m.flash = "no drill is running; a investigates a running drill without starting one"
			return m, nil
		}
		return m.startAssistant(AssistRequest{Kind: "drill", Name: run.Drill})
	}
	if m.opts.Assistant == nil {
		m.flash = "assistant unavailable; local mode requires the ai stack"
		return m, nil
	}
	m = m.newAssistPane()
	m.assistant.picking, m.assistant.stage = true, "loading firing alerts"
	api, ctx, id := m.opts.Assistant, m.assistant.ctx, m.assistant.id
	return m, func() tea.Msg {
		alerts, err := api.Alerts(ctx)
		return assistAlertsMsg{id: id, alerts: alerts, err: err}
	}
}

func (m Model) newAssistPane() Model {
	m.cancelAssistant()
	m.assistID++
	ctx, cancel := context.WithCancel(context.Background())
	m.assistant = assistPane{open: true, state: assistRunning, id: m.assistID, ctx: ctx, cancel: cancel,
		events: make(chan tea.Msg, 1), text: newRing(logLines), view: viewer{lines: newRing(logLines)}}
	return m
}

func (m Model) startAssistant(req AssistRequest) (tea.Model, tea.Cmd) {
	command := req.Command(m.opts.AssistFlags...)
	if m.opts.Assistant == nil {
		m.flash = "assistant unavailable; run it in a shell: $ " + command
		return m, nil
	}
	m = m.newAssistPane()
	m.assistant.command, m.assistant.stage = command, "connecting"
	m.assistant.text.add("$ " + command)
	m.assistant.reflow(m.width)
	m.flash = statusProgressing.symbol() + " $ " + command
	api, ctx, out, id := m.opts.Assistant, m.assistant.ctx, m.assistant.events, m.assistant.id
	return m, tea.Batch(func() tea.Msg {
		streamAssistant(ctx, api, req, id, out, redrawEvery)
		return nil
	}, m.listenAssistant())
}

func (m Model) listenAssistant() tea.Cmd {
	ctx, out := m.assistant.ctx, m.assistant.events
	return func() tea.Msg {
		select {
		case msg := <-out:
			return msg
		case <-ctx.Done():
			return nil
		}
	}
}

// Only this command owns pending progress. The callback uses a bounded
// channel; cancellation releases both it and the listener.
func streamAssistant(ctx context.Context, api Assistant, req AssistRequest, id int, out chan<- tea.Msg, every time.Duration) {
	progress := make(chan assist.Progress, 32)
	results := make(chan assistResult, 1)
	go func() {
		note, err := api.Run(ctx, req, func(p assist.Progress) {
			select {
			case progress <- p:
			case <-ctx.Done():
			}
		})
		results <- assistResult{note: note, err: err}
		close(progress)
	}()
	timer := time.NewTimer(every)
	defer timer.Stop()
	var pending []assist.Progress
	var result *assistResult
	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-progress:
			if !ok {
				progress = nil
			} else if len(pending) < logLines {
				pending = append(pending, p)
			}
		case done := <-results:
			result = &done
			results = nil
		case <-timer.C:
			// Drain all final observations before publishing the result.
			if result != nil && progress != nil {
				for p := range progress {
					if len(pending) < logLines {
						pending = append(pending, p)
					}
				}
				progress = nil
			}
			if len(pending) == 0 && result == nil {
				timer.Reset(every)
				continue
			}
			select {
			case out <- assistMsg{id: id, batch: pending, result: result}:
			case <-ctx.Done():
				return
			}
			pending = nil
			if result != nil {
				return
			}
			timer.Reset(every)
		}
	}
}

func (m *Model) cancelAssistant() {
	if m.assistant.cancel != nil {
		m.assistant.cancel()
		m.assistant.cancel = nil
		if m.assistant.state == assistRunning {
			m.assistant.state, m.assistant.stage = assistCancelled, ""
		}
	}
}

func (m Model) assistantUpdate(msg assistMsg) (tea.Model, tea.Cmd) {
	p := &m.assistant
	if msg.id != p.id || p.state != assistRunning {
		return m, nil
	}
	for _, update := range msg.batch {
		p.stage = update.Stage
		p.text.add(update.Stage)
		if update.Evidence != nil {
			e := update.Evidence
			p.text.add(fmt.Sprintf("[%s] %s: %s", e.ID, e.Source, e.Data))
		}
		if update.Candidate != nil {
			p.note.Candidate = update.Candidate
			p.text.add("Rule candidate: " + claimText(*update.Candidate))
		}
	}
	if msg.result != nil {
		p.note, p.state = msg.result.note, assistDone
		switch {
		case msg.result.err != nil || p.note.Incomplete:
			p.state = assistError
		case p.note.Abstained():
			p.state = assistAbstained
		}
		p.stage = ""
		p.text = noteText(p.note, p.command)
		if msg.result.err != nil {
			p.text.add("Error: " + assist.Redact(msg.result.err.Error()))
		}
		m.cancelAssistant()
		p.reflow(m.width)
		return m, nil
	}
	p.reflow(m.width)
	return m, m.listenAssistant()
}

func (m Model) assistantAlerts(msg assistAlertsMsg) (tea.Model, tea.Cmd) {
	p := &m.assistant
	if msg.id != p.id || p.state != assistRunning {
		return m, nil
	}
	p.stage, p.state = "", assistIdle
	switch {
	case msg.err != nil:
		p.state, p.picking = assistError, false
		p.text.add("Error: " + assist.Redact(msg.err.Error()))
	case len(msg.alerts) == 0:
		p.state, p.picking = assistError, false
		p.text.add("No firing alerts.")
	default:
		p.alerts = msg.alerts
	}
	m.cancelAssistant()
	p.reflow(m.width)
	return m, nil
}

func (m Model) assistantKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.assistant
	k := msg.String()
	switch {
	case k == "esc" && p.state == assistRunning:
		m.cancelAssistant()
		p.text.add("Cancelled; no commands were executed.")
		p.reflow(m.width)
		return m, nil
	case k == "esc" || k == "q":
		m.cancelAssistant()
		p.open = false
		return m, nil
	case p.picking && p.state == assistIdle:
		switch k {
		case "j", "down":
			p.cursor = min(p.cursor+1, len(p.alerts)-1)
		case "k", "up":
			p.cursor = max(0, p.cursor-1)
		case "a", "enter":
			alert := p.alerts[p.cursor]
			return m.startAssistant(AssistRequest{Kind: "alert", Name: alert.Name, Labels: alert.Labels})
		}
	default:
		p.view.key(msg, m.bodyHeight())
	}
	return m, nil
}

func claimText(c assist.Claim) string {
	text := c.Text
	if c.CauseID != "" {
		text = c.CauseID + ": " + text
	}
	if len(c.Evidence) > 0 {
		text += " [" + strings.Join(c.Evidence, ", ") + "]"
	}
	if c.Unsupported {
		text += " [unsupported]"
	}
	return text
}

func noteText(note assist.Note, command string) *ring {
	lines := newRing(logLines)
	if command != "" {
		lines.add("$ "+command, "")
	}
	if note.Candidate != nil {
		lines.add("Rule candidate: " + claimText(*note.Candidate))
	} else {
		lines.add("Rule candidate: none")
	}
	for _, section := range []struct {
		name   string
		claims []assist.Claim
	}{{"What fired", note.WhatFired}, {"Likely cause", note.Cause}, {"Commands to confirm (not executed)", note.Confirm}, {"Proposed fix (not executed)", note.Fix}} {
		lines.add("", section.name)
		for _, c := range section.claims {
			lines.add(claimText(c))
		}
	}
	lines.add("", "Cited evidence")
	for _, e := range note.Evidence {
		lines.add(fmt.Sprintf("[%s] %s %s: %s", e.ID, e.Source, e.At.Format(time.RFC3339), e.Data))
	}
	if note.TraceID != "" {
		lines.add("", "Langfuse trace: "+note.TraceID)
	}
	if note.Incomplete {
		lines.add("Incomplete: " + note.Error)
	}
	return lines
}

func paneText(text string) string {
	return strings.Map(func(r rune) rune {
		if (r < 32 && r != '\n' && r != '\t') || r == 127 {
			return -1
		}
		return r
	}, ansi.Strip(text))
}

func paneLine(text string) string { return strings.Join(strings.Fields(paneText(text)), " ") }

func (p *assistPane) reflow(width int) {
	lines := newRing(logLines)
	for _, line := range p.text.slice(0, p.text.len()) {
		lines.add(strings.Split(wrap(paneText(line), max(1, width-2)), "\n")...)
	}
	p.view.lines = lines
	p.view.top = min(p.view.top, max(0, lines.len()-1))
}

func (p assistPane) render(height int) string {
	title := "Assistant: " + p.state.String()
	if p.command != "" {
		title += " · $ " + paneLine(p.command)
	}
	var b strings.Builder
	b.WriteString(bold.Render(" "+title) + "\n")
	b.WriteString(warn.Render(" hypothesis, needs human review") + " · " + p.stage + "\n")
	if p.picking && p.state == assistIdle {
		top := max(0, p.cursor-viewerRows(height)+1)
		for i := top; i < min(len(p.alerts), top+viewerRows(height)); i++ {
			alert := p.alerts[i]
			mark := " "
			if i == p.cursor {
				mark = ">"
			}
			fmt.Fprintf(&b, " %s %s · %s/%s\n", mark, paneLine(alert.Name), paneLine(alert.Labels["namespace"]), paneLine(alert.Labels["pod"]))
		}
		return b.String()
	}
	rows, top := viewerRows(height), p.view.top
	if p.view.atEnd {
		top = max(0, p.view.lines.len()-rows)
	}
	for _, line := range p.view.lines.slice(top, top+rows) {
		b.WriteString(" " + line + "\n")
	}
	return b.String()
}
