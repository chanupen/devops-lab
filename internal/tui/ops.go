package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/chanupen/devops-lab/internal/state"
)

// opsModel is screen 5: every operation in state.json, newest first, with
// the running one on top. enter opens a row's full output.
type opsModel struct {
	cursor int
}

// opsRows is the number of rows: the running operation, then the log.
func opsRows(d *data, running bool) int {
	n := len(d.st.Ops)
	if running {
		n++
	}
	return n
}

func (o opsModel) update(msg tea.KeyMsg, n int) opsModel {
	switch msg.String() {
	case "j", "down":
		o.cursor = min(o.cursor+1, max(0, n-1))
	case "k", "up":
		o.cursor = max(o.cursor-1, 0)
	case "g", "home":
		o.cursor = 0
	case "G", "end":
		o.cursor = max(0, n-1)
	}
	return o
}

// selected returns the op under the cursor, or nil for the running row.
func (o opsModel) selected(d *data, running bool) *state.Op {
	i := o.cursor
	if running {
		if i == 0 {
			return nil
		}
		i--
	}
	if i < 0 || i >= len(d.st.Ops) {
		return nil
	}
	op := d.st.Ops[len(d.st.Ops)-1-i]
	return &op
}

func (o opsModel) view(d *data, op *opState, now time.Time, height int) string {
	var b strings.Builder
	b.WriteString(bold.Render(" Ops log") + faint.Render(fmt.Sprintf("  %d operations, newest first  ·  enter opens the output", len(d.st.Ops))) + "\n")
	type row struct{ sym, when, command, stack, action, status, took, note string }
	var rows []row
	if op != nil {
		rows = append(rows, row{statusProgressing.symbol(), "now", op.req.Command(), "", "", "running",
			now.Sub(op.started).Round(time.Second).String(), op.step})
	}
	for i := len(d.st.Ops) - 1; i >= 0; i-- {
		o := d.st.Ops[i]
		r := row{opStatus(o).symbol(), o.Started.Local().Format("Jan 02 15:04:05"), o.Command, o.Stack, o.Action,
			o.Status, o.Finished.Sub(o.Started).Round(100 * time.Millisecond).String(), ""}
		if o.Error != "" {
			r.note = firstLine(o.Error)
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		b.WriteString(faint.Render("  none yet; press U to bring the lab up") + "\n")
		return b.String()
	}
	w := struct{ when, command, stack, action, status, took int }{}
	for _, r := range rows {
		w.when, w.command, w.stack = max(w.when, len(r.when)), max(w.command, len(r.command)), max(w.stack, len(r.stack))
		w.action, w.status, w.took = max(w.action, len(r.action)), max(w.status, len(r.status)), max(w.took, len(r.took))
	}
	visible := max(1, height-1)
	cursor := min(o.cursor, len(rows)-1)
	start := max(0, cursor-visible+1)
	for i := start; i < len(rows) && i < start+visible; i++ {
		r := rows[i]
		line := fmt.Sprintf(" %-*s  %-*s  %-*s  %-*s  %-*s  %*s ", w.when, r.when, w.command, r.command,
			w.stack, r.stack, w.action, r.action, w.status, r.status, w.took, r.took)
		if i == cursor {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		b.WriteString(" " + r.sym + line)
		if r.note != "" {
			b.WriteString(" " + faint.Render(r.note))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// openOp opens the output of the selected row: the live output of the
// running operation, or a finished op's log file.
func (m Model) openOp() (Model, tea.Cmd) {
	running := m.op != nil
	sel := m.ops.selected(&m.d, running)
	if sel == nil {
		if !running {
			return m, nil
		}
		m.view = &viewer{title: "$ " + m.op.req.Command(), sub: "running · output so far, following", lines: m.op.lines, atEnd: true, live: true}
		return m, nil
	}
	op := *sel
	sub := fmt.Sprintf("%s %s · %s in %s", op.Stack, op.Action, op.Status, op.Finished.Sub(op.Started).Round(100*time.Millisecond))
	if op.Log == "" {
		m.view = &viewer{title: "$ " + op.Command, sub: sub, lines: newRing(logLines), err: "no log was recorded for this operation"}
		return m, nil
	}
	m.view = &viewer{title: "$ " + op.Command, sub: sub + " · " + op.Log, lines: newRing(logLines), loading: true, atEnd: true}
	m.viewPath = op.Log
	return m, readLog(op.Log)
}
