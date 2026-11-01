// Package termbg stops Bubble Tea v1 from querying the terminal at start-up.
//
// Bubble Tea's package init calls lipgloss.HasDarkBackground, which sends an
// OSC 11 background-colour query and waits for the reply. Over SSH that is a
// round trip before main() runs (about 28 ms from the Mac), and a terminal
// that never answers stalls start-up for termenv's 5 s timeout. The lab uses
// no adaptive colours, so the answer is never needed.
//
// Setting the background explicitly makes lipgloss skip the query. This must
// happen before Bubble Tea's init. Go initialises packages whose imports are
// ready in import-path order (Go spec, "Package initialization", since 1.21);
// this package imports only lipgloss and its path sorts before
// github.com/charmbracelet/bubbletea, so its init runs first.
// TestRunsBeforeBubbleTea guards the ordering.
package termbg

import "github.com/charmbracelet/lipgloss"

func init() {
	lipgloss.SetHasDarkBackground(true)
}
