package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/state"
)

// labsModel is screen 3: the labs with your progress. enter opens a step's
// text, v verifies it, h shows its next hint, n/p move between steps.
type labsModel struct {
	cursor int
	open   bool // the step text is showing
	step   int  // the step shown while open
	scroll int  // first line of the step text shown
	busy   bool // a verify or hint runs
	// last verify result, by "id/step"; a map, so copies share it
	results map[string]labs.Result
}

func newLabsModel() labsModel { return labsModel{results: map[string]labs.Result{}} }

// Messages from the labs actions.
type (
	labVerifyMsg struct {
		id   string
		step int
		res  labs.Result
		err  error
	}
	labHintMsg struct {
		step int
		hint string
		err  error
	}
)

func (lm labsModel) selected(d *data) (labs.Lab, state.LabProgress, bool) {
	if lm.cursor < 0 || lm.cursor >= len(d.labs) {
		return labs.Lab{}, state.LabProgress{}, false
	}
	l := d.labs[lm.cursor]
	return l, d.st.Labs[l.ID], true
}

// currentStep is the step to show for progress p: the next one to do, or
// the last once the lab is done.
func currentStep(l labs.Lab, p state.LabProgress) int {
	return min(max(p.Step, 1), len(l.Steps))
}

// key handles the Labs screen's keys.
func (m Model) labsKey(k string) (tea.Model, tea.Cmd) {
	lm := &m.labs
	l, p, ok := lm.selected(&m.d)
	switch k {
	case "j", "down":
		if lm.open {
			lm.scroll++
		} else {
			lm.cursor = min(lm.cursor+1, max(0, len(m.d.labs)-1))
		}
	case "k", "up":
		if lm.open {
			lm.scroll = max(lm.scroll-1, 0)
		} else {
			lm.cursor = max(lm.cursor-1, 0)
		}
	case "enter":
		if ok && len(l.Steps) > 0 {
			lm.open, lm.step, lm.scroll = true, currentStep(l, p), 0
		}
	case "esc":
		lm.open = false
	case "n", "p":
		if !ok || len(l.Steps) == 0 {
			break
		}
		switch {
		case !lm.open:
			lm.open, lm.step = true, currentStep(l, p)
		case k == "n":
			lm.step = min(lm.step+1, len(l.Steps))
		default:
			lm.step = max(lm.step-1, 1)
		}
		lm.scroll = 0
	case "v", "h":
		if !ok {
			break
		}
		step := currentStep(l, p)
		if lm.open {
			step = lm.step
		}
		return m.labAction(k, l, step)
	}
	return m, nil
}

// labAction runs a verify or a hint for the started lab, in the background.
func (m Model) labAction(k string, l labs.Lab, step int) (tea.Model, tea.Cmd) {
	verb := map[string]string{"v": "verify", "h": "hint"}[k]
	command := fmt.Sprintf("lab labs %s %d", verb, step)
	switch {
	case m.d.st.ActiveLab != l.ID:
		m.flash = fmt.Sprintf("lab %s is not started: $ lab labs start %s", l.Number(), l.Number())
		return m, nil
	case m.labs.busy:
		m.flash = "busy: one lab check at a time"
		return m, nil
	case k == "v" && m.opts.LabVerify == nil, k == "h" && m.opts.LabHint == nil:
		m.flash = "run it in a shell: $ " + command
		return m, nil
	}
	m.labs.busy = true
	m.flash = statusProgressing.symbol() + " $ " + command
	if k == "h" {
		hint := m.opts.LabHint
		return m, func() tea.Msg {
			_, h, err := hint(step)
			return labHintMsg{step: step, hint: h, err: err}
		}
	}
	verify := m.opts.LabVerify
	return m, func() tea.Msg {
		res, err := verify(context.Background(), step)
		return labVerifyMsg{id: l.ID, step: step, res: res, err: err}
	}
}

// labDone records a verify or hint result and reloads the progress the
// labs manager wrote.
func (m Model) labDone(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.labs.busy = false
	switch msg := msg.(type) {
	case labVerifyMsg:
		command := fmt.Sprintf("$ lab labs verify %d", msg.step)
		switch {
		case msg.err != nil:
			m.flash = statusFailed.symbol() + " " + command + ": " + firstLine(msg.err.Error())
		case msg.res.Passed:
			m.labs.results[fmt.Sprintf("%s/%d", msg.id, msg.step)] = msg.res
			m.flash = statusHealthy.symbol() + " " + command + ": step done"
		default:
			m.labs.results[fmt.Sprintf("%s/%d", msg.id, msg.step)] = msg.res
			m.flash = statusFailed.symbol() + " " + command + ": " + msg.res.Reason
		}
	case labHintMsg:
		command := fmt.Sprintf("$ lab labs hint %d", msg.step)
		if msg.err != nil {
			m.flash = statusFailed.symbol() + " " + command + ": " + firstLine(msg.err.Error())
		} else {
			m.flash = command
			m.labs.open, m.labs.step = true, msg.step
		}
	}
	return m, m.loadState()
}

// view renders the list on the left and, on the right, the selected lab or
// the open step's text, wrapped to width.
func (lm labsModel) view(d *data, now time.Time, height int) (left, right func(w int) string) {
	left = func(int) string {
		if len(d.labs) == 0 {
			return " no labs in labs/"
		}
		var b strings.Builder
		b.WriteString(bold.Render(" Labs") + "\n")
		for i, l := range d.labs {
			p := d.st.Labs[l.ID]
			row := fmt.Sprintf(" %s %s  %s  %d/%d", labMarkStyled(labs.LabMark(l, p)), l.Number(), l.Title, labs.DoneSteps(l, p), len(l.Steps))
			if i == lm.cursor {
				row = lipgloss.NewStyle().Reverse(true).Render(row)
			}
			b.WriteString(row + "\n")
		}
		return b.String()
	}
	right = func(w int) string {
		l, p, ok := lm.selected(d)
		if !ok {
			return ""
		}
		var text string
		if lm.open {
			text = lm.stepText(l, p, w)
		} else {
			text = lm.labText(d, l, p, now, w)
		}
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		top := min(lm.scroll, max(0, len(lines)-height+1))
		lines = lines[top:]
		if len(lines) > height {
			lines = append(lines[:height-1], faint.Render(" ↓ more: j scrolls"))
		}
		return strings.Join(lines, "\n")
	}
	return left, right
}

func labMarkStyled(k labs.Mark) string {
	return [...]status{statusOff, statusProgressing, statusHealthy}[k].symbol()
}

var codeSpan = regexp.MustCompile("`([^`]+)`")

// wrap word-wraps markdown text s to w cells with a one-cell margin,
// keeping its line breaks, with `code` spans styled instead of quoted.
func wrap(s string, w int) string { return hanging("", s, w) }

// hanging wraps s like wrap, with prefix on its first line and the
// following lines indented to match.
func hanging(prefix, s string, w int) string {
	s = codeSpan.ReplaceAllStringFunc(s, func(c string) string { return code.Render(strings.Trim(c, "`")) })
	pad := 1 + ansi.StringWidth(prefix)
	body := lipgloss.NewStyle().Width(max(w, 20)).PaddingLeft(pad).Render(s)
	if prefix == "" {
		return body
	}
	return " " + prefix + body[pad:]
}

func (lm labsModel) labText(d *data, l labs.Lab, p state.LabProgress, now time.Time, w int) string {
	var b strings.Builder
	b.WriteString(bold.Render(" "+l.Title) + "\n")
	needs := "its own cluster, " + labs.ClusterName(l)
	if l.Needs == labs.NeedsLab {
		needs = "the main lab (" + strings.Join(l.Stacks, ", ") + ")"
	}
	fmt.Fprintf(&b, " %s · about %s · %s · runs on %s\n", l.ID, l.Time, l.Level, needs)
	spent := labs.TimeSpent(p, d.labUptime(l), now)
	switch {
	case d.st.ActiveLab == l.ID:
		if u, ok := d.unit(UnitScratch, labs.ClusterName(l)); ok && !u.Running {
			fmt.Fprintf(&b, " %s started · cluster %s stopped · %s spent · %s\n", statusProgressing.symbol(), statusOff.symbol(),
				shortSpent(spent), faint.Render("$ "+u.Fix))
			break
		}
		fmt.Fprintf(&b, " %s started · %s spent\n", statusProgressing.symbol(), shortSpent(spent))
	case !p.Completed.IsZero():
		fmt.Fprintf(&b, " %s done · %s spent\n", statusHealthy.symbol(), shortSpent(spent))
	case p.Step > 0:
		fmt.Fprintf(&b, " %s stopped at step %d · %s spent · %s\n", statusOff.symbol(), p.Step, shortSpent(spent),
			faint.Render("$ lab labs start "+l.Number()))
	default:
		fmt.Fprintf(&b, " %s not started · %s\n", statusOff.symbol(), faint.Render("$ lab labs start "+l.Number()))
	}
	b.WriteString("\n")
	for _, s := range l.Steps {
		notes := ""
		if a := p.Attempts[s.N]; a > 0 {
			notes = faint.Render(fmt.Sprintf("  %d×", a))
		}
		fmt.Fprintf(&b, " %s %d  %s%s\n", labMarkStyled(labs.StepMark(p, s.N)), s.N, s.Title, notes)
	}
	b.WriteString("\n" + wrap(firstParagraph(l.Intro), w-1) + "\n")
	return b.String()
}

func (lm labsModel) stepText(l labs.Lab, p state.LabProgress, w int) string {
	s, _ := l.Step(lm.step)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", bold.Render(fmt.Sprintf(" Step %d of %d: %s", s.N, len(l.Steps), s.Title)))
	marks := make([]string, 0, len(l.Steps))
	for _, x := range l.Steps {
		marks = append(marks, labMarkStyled(labs.StepMark(p, x.N)))
	}
	b.WriteString(" " + strings.Join(marks, " ") + "\n")
	if res, ok := lm.results[fmt.Sprintf("%s/%d", l.ID, s.N)]; ok {
		if res.Passed {
			b.WriteString(" " + statusHealthy.symbol() + " verified: " + lastLineOf(res.Output) + "\n")
		} else {
			b.WriteString(hanging(statusFailed.symbol()+" ", "not done yet: "+res.Reason, w-1) + "\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(wrap(s.Task, w-1) + "\n\n")
	shown := min(p.Hints[s.N], len(s.Hints))
	if shown > 0 {
		b.WriteString(bold.Render(" Hints") + "\n")
		for i := range shown {
			b.WriteString(hanging(fmt.Sprintf("%d. ", i+1), s.Hints[i], w-1) + "\n")
		}
		b.WriteString("\n")
	}
	if left := len(s.Hints) - shown; left > 0 {
		b.WriteString(faint.Render(fmt.Sprintf(" h shows a hint (%d left)", left)) + "\n\n")
	}
	b.WriteString(bold.Render(" Expected result") + "\n" + wrap(s.Expected, w-1) + "\n")
	return b.String()
}

func firstParagraph(s string) string {
	p, _, _ := strings.Cut(strings.TrimSpace(s), "\n\n")
	return p
}

func lastLineOf(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func shortSpent(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}
