package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	symbol = map[Status]string{OK: "●", Warn: "◐", Fail: "✕", Skip: "○", Stopped: "○"}
	colour = map[Status]lipgloss.Color{OK: "2", Warn: "3", Fail: "1", Skip: "8", Stopped: "8"}
)

// Report is the --json document.
type Report struct {
	Profile string   `json:"profile"`
	OK      bool     `json:"ok"`
	Results []Result `json:"results"`
}

// WriteJSON writes results as an indented Report.
func WriteJSON(w io.Writer, profile string, results []Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(Report{Profile: profile, OK: !Failed(results), Results: results})
}

// WriteTable writes a coloured table, with a fix hint under each failure or warning.
// The renderer decides whether colour is emitted (none when output is not a terminal).
func WriteTable(w io.Writer, r *lipgloss.Renderer, results []Result) error {
	var b strings.Builder
	width := 0
	for _, res := range results {
		width = max(width, len(res.Name))
	}
	counts := map[Status]int{}
	for _, res := range results {
		counts[res.Status]++
		st := r.NewStyle().Foreground(colour[res.Status])
		fmt.Fprintf(&b, "%s %-*s  %s\n", st.Render(symbol[res.Status]), width, res.Name, res.Detail)
		if res.Fix != "" && (res.Status == Fail || res.Status == Warn || res.Status == Stopped) {
			fmt.Fprintf(&b, "  %s  %s\n", strings.Repeat(" ", width), r.NewStyle().Faint(true).Render("fix: "+res.Fix))
		}
	}
	summary := fmt.Sprintf("%d ok, %d warn, %d fail", counts[OK], counts[Warn], counts[Fail])
	if counts[Stopped] > 0 {
		summary += fmt.Sprintf(", %d stopped", counts[Stopped])
	}
	if counts[Skip] > 0 {
		summary += fmt.Sprintf(", %d skipped", counts[Skip])
	}
	fmt.Fprintf(&b, "\n%s\n", r.NewStyle().Bold(true).Render(summary))
	_, err := io.WriteString(w, b.String())
	return err
}
