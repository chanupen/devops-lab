package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// paletteEntry is one command the palette offers, with its CLI equivalent.
type paletteEntry struct {
	label  string // ":up obs"
	cli    string // "lab up --stacks obs"
	req    *Request
	report string // status, doctor or plan
}

// palette is the : command palette: type to fuzzy-match, enter runs, esc
// closes.
type palette struct {
	input   textinput.Model
	entries []paletteEntry
	cursor  int
}

func newPalette(order []string) *palette {
	in := textinput.New()
	in.Prompt = ":"
	in.Focus()
	var es []paletteEntry
	add := func(label string, req *Request, report string) {
		cli := "lab " + report
		if req != nil {
			cli = req.Command()
		}
		es = append(es, paletteEntry{label: label, cli: cli, req: req, report: report})
	}
	add(":up", &Request{}, "")
	for _, s := range order {
		add(":up "+s, &Request{Stacks: []string{s}}, "")
	}
	for _, s := range order {
		add(":down "+s, &Request{Down: true, Stacks: []string{s}}, "")
	}
	add(":status", nil, "status")
	add(":doctor", nil, "doctor")
	add(":plan", nil, "plan")
	return &palette{input: in, entries: es}
}

// matches returns the entries that fuzzy-match the input, best first.
func (p *palette) matches() []paletteEntry {
	q := strings.TrimSpace(p.input.Value())
	type scored struct {
		e     paletteEntry
		score int
		i     int
	}
	var out []scored
	for i, e := range p.entries {
		if s, ok := fuzzyScore(strings.TrimPrefix(e.label, ":"), q); ok {
			out = append(out, scored{e, s, i})
		}
	}
	slices.SortStableFunc(out, func(a, b scored) int { return b.score - a.score })
	es := make([]paletteEntry, len(out))
	for i, s := range out {
		es[i] = s.e
	}
	return es
}

// fuzzyScore matches q as a subsequence of s, ignoring case and spaces.
// Matches that start a word or follow the previous match score higher.
func fuzzyScore(s, q string) (int, bool) {
	q = strings.ToLower(strings.ReplaceAll(q, " ", ""))
	if q == "" {
		return 0, true
	}
	ls := strings.ToLower(s)
	score, qi, prev := 0, 0, -2
	for i := 0; i < len(ls) && qi < len(q); i++ {
		if ls[i] != q[qi] {
			continue
		}
		switch {
		case i == prev+1:
			score += 3
		case i == 0 || !unicode.IsLetter(rune(ls[i-1])):
			score += 2
		default:
			score++
		}
		prev, qi = i, qi+1
	}
	if qi < len(q) {
		return 0, false
	}
	return score, true // ties keep the list order: catalog order, :up first
}

func (p *palette) key(msg tea.KeyMsg) tea.Cmd {
	n := len(p.matches())
	switch msg.String() {
	case "down", "ctrl+n", "tab":
		p.cursor = min(p.cursor+1, max(0, n-1))
		return nil
	case "up", "ctrl+p", "shift+tab":
		p.cursor = max(p.cursor-1, 0)
		return nil
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.cursor = 0
	return cmd
}

// selected returns the highlighted entry, if any.
func (p *palette) selected() (paletteEntry, bool) {
	ms := p.matches()
	if len(ms) == 0 {
		return paletteEntry{}, false
	}
	return ms[min(p.cursor, len(ms)-1)], true
}

func (p *palette) render(height int) string {
	var b strings.Builder
	b.WriteString(" " + p.input.View() + "\n")
	ms := p.matches()
	if len(ms) == 0 {
		b.WriteString(faint.Render("  no command matches") + "\n")
		return b.String()
	}
	w := 0
	for _, e := range ms {
		w = max(w, len(e.label))
	}
	rows := max(1, height-1)
	cursor := min(p.cursor, len(ms)-1)
	start := max(0, cursor-rows+1)
	for i := start; i < len(ms) && i < start+rows; i++ {
		e := ms[i]
		l := fmt.Sprintf(" %-*s ", w, e.label)
		if i == cursor {
			l = lipgloss.NewStyle().Reverse(true).Render(l)
		}
		b.WriteString(" " + l + "  " + faint.Render("$ "+e.cli) + "\n")
	}
	return b.String()
}
