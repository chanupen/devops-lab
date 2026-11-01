package tui

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// viewer shows a log or a command's output over the body, with the
// equivalent CLI command at the top. It keeps the last logLines lines.
type viewer struct {
	title   string // "$ lab up --stacks obs"
	sub     string // one faint line under the title
	lines   *ring
	top     int  // first line shown
	atEnd   bool // keep the last line in view as lines arrive
	live    bool // lines come from the running operation
	loading bool
	err     string
}

// logMsg carries a log file read in the background.
type logMsg struct {
	path  string
	lines *ring
	err   error
}

// readLog reads a step log off the key loop, keeping its last logLines lines.
func readLog(path string) tea.Cmd {
	return func() tea.Msg {
		f, err := os.Open(path)
		if err != nil {
			return logMsg{path: path, err: err}
		}
		defer func() { _ = f.Close() }()
		r := newRing(logLines)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			r.add(sc.Text())
		}
		return logMsg{path: path, lines: r, err: sc.Err()}
	}
}

// rows is how many log lines fit under the title.
func viewerRows(height int) int { return max(1, height-2) }

func (v *viewer) key(msg tea.KeyMsg, height int) {
	rows := viewerRows(height)
	last := max(0, v.lines.len()-rows)
	if v.atEnd {
		v.top = last
	}
	switch msg.String() {
	case "j", "down":
		v.top++
	case "k", "up":
		v.top--
	case "pgdown", " ", "f":
		v.top += rows
	case "pgup", "b":
		v.top -= rows
	case "g", "home":
		v.top = 0
	case "G", "end":
		v.top = last
	}
	v.top = min(max(0, v.top), last)
	v.atEnd = v.top == last
}

func (v *viewer) render(height int) string {
	var b strings.Builder
	b.WriteString(bold.Render(" "+v.title) + "\n")
	sub := v.sub
	switch {
	case v.loading:
		sub = "loading…"
	case v.err != "":
		sub = statusFailed.symbol() + " " + v.err
	case v.lines.dropped() > 0:
		sub += fmt.Sprintf("  (first %d lines dropped; the file has them all)", v.lines.dropped())
	}
	b.WriteString(faint.Render(" "+sub) + "\n")
	rows := viewerRows(height)
	top := v.top
	if v.atEnd {
		top = max(0, v.lines.len()-rows)
	}
	for _, l := range v.lines.slice(top, top+rows) {
		b.WriteString(" " + l + "\n")
	}
	return b.String()
}
