package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/gitops"
)

// stacksModel is screen 2: the catalog on the left, the selected stack's
// dependencies and Argo CD apps on the right.
type stacksModel struct {
	cursor    int
	filter    textinput.Model
	filtering bool
}

func newStacksModel() stacksModel {
	f := textinput.New()
	f.Prompt = "/"
	return stacksModel{filter: f}
}

// visible returns the stacks that match the filter, in dependency order.
func (s stacksModel) visible(d *data) []string {
	q := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	if q == "" {
		return d.order
	}
	var out []string
	for _, name := range d.order {
		if strings.Contains(name, q) {
			out = append(out, name)
		}
	}
	return out
}

// selected returns the stack under the cursor, or "".
func (s stacksModel) selected(d *data) string {
	v := s.visible(d)
	if len(v) == 0 {
		return ""
	}
	return v[min(s.cursor, len(v)-1)]
}

func (s stacksModel) update(msg tea.KeyMsg, d *data) (stacksModel, tea.Cmd) {
	n := len(s.visible(d))
	if s.filtering {
		switch msg.String() {
		case "esc":
			s.filter.SetValue("")
			s.filtering = false
			s.filter.Blur()
		case "enter":
			s.filtering = false
			s.filter.Blur()
		default:
			var cmd tea.Cmd
			s.filter, cmd = s.filter.Update(msg)
			s.cursor = 0
			return s, cmd
		}
		return s, nil
	}
	switch msg.String() {
	case "j", "down":
		s.cursor = min(s.cursor+1, max(0, n-1))
	case "k", "up":
		s.cursor = max(s.cursor-1, 0)
	case "g", "home":
		s.cursor = 0
	case "G", "end":
		s.cursor = max(0, n-1)
	case "/":
		s.filtering = true
		return s, s.filter.Focus()
	case "esc":
		s.filter.SetValue("")
		s.cursor = 0
	}
	return s, nil
}

func (s stacksModel) view(d *data, height int) (left, right string) {
	v := s.visible(d)
	cursor := min(s.cursor, max(0, len(v)-1))
	var l strings.Builder
	title := bold.Render(" Stacks")
	if s.filtering || s.filter.Value() != "" {
		title += "  " + s.filter.View()
	}
	l.WriteString(title + "\n")
	rows := max(1, height-1)
	start := max(0, cursor-rows+1)
	width := 0
	for _, name := range d.order {
		width = max(width, len(name))
	}
	row := func(name string, selected bool) string {
		st, text := stackStatus(name, d.st, d.live)
		n := fmt.Sprintf(" %-*s ", width, name)
		if selected {
			n = lipgloss.NewStyle().Reverse(true).Render(n)
		}
		return " " + st.symbol() + n + " " + faint.Render(text)
	}
	for i := start; i < len(v) && i < start+rows; i++ {
		l.WriteString(row(v[i], i == cursor) + "\n")
	}
	if len(v) == 0 {
		l.WriteString(faint.Render("  no stack matches") + "\n")
	}
	// The pane sizes to its widest line; measure the whole catalog so the
	// divider stays put while a filter narrows the list.
	full := 0
	for _, name := range d.order {
		full = max(full, ansi.StringWidth(row(name, false)))
	}
	l.WriteString(strings.Repeat(" ", full))
	return l.String(), stackDetail(d, s.selected(d))
}

// stackDetail is the right pane for one stack.
func stackDetail(d *data, name string) string {
	if name == "" {
		return ""
	}
	var b strings.Builder
	row := func(k, v string) { fmt.Fprintf(&b, "  %s %s\n", faint.Render(fmt.Sprintf("%-11s", k)), v) }
	st, text := stackStatus(name, d.st, d.live)
	fmt.Fprintf(&b, " %s  %s %s\n\n", bold.Render(name), st.symbol(), st.word())
	row("state", text)
	deps := strings.Join(d.deps[name], ", ")
	if deps == "" {
		deps = "—"
	}
	row("depends on", deps)
	if ss, ok := d.st.Stacks[name]; ok {
		if !ss.UpdatedAt.IsZero() {
			row("updated", ss.UpdatedAt.Local().Format("2006-01-02 15:04"))
		}
		if ss.Error != "" {
			row("error", firstLine(ss.Error))
		}
	}
	if name == engine.RevisionStack {
		row("revision", revision(d))
	}
	if d.live.known && !d.live.up && d.st.Stacks[name].Status != "" {
		b.WriteString("\n  " + faint.Render("The cluster is off ("+d.live.reason+"); "+emptyHint+".") + "\n")
	}
	if infraStacks[name] || !d.live.up {
		return b.String()
	}
	apps := d.live.stackApps(name)
	b.WriteString("\n " + bold.Render("Argo CD apps") + "\n")
	if len(apps) == 0 {
		b.WriteString(faint.Render("  none yet") + "\n")
		return b.String()
	}
	w := 0
	for _, a := range apps {
		w = max(w, len(a.Name))
	}
	for _, a := range apps {
		sync, health := a.Sync, a.Health
		if sync == "" {
			sync = "—"
		}
		if health == "" {
			health = "—"
		}
		fmt.Fprintf(&b, "  %s %-*s  %-10s %s\n", appStatus(a).symbol(), w, a.Name, sync, health)
		// The first condition says why; the rest are usually the same cause.
		if len(a.Problems) > 0 {
			b.WriteString("     " + faint.Render(firstLine(a.Problems[0])) + "\n")
			if more := len(a.Problems) - 1; more > 0 {
				b.WriteString("     " + faint.Render(fmt.Sprintf("+%d more %s", more, plural(more, "condition", "conditions"))) + "\n")
			}
		}
	}
	if name == "gitops" {
		b.WriteString(faint.Render("  "+gitops.RootApp+" renders every other stack's apps") + "\n")
	}
	return b.String()
}
