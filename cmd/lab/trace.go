package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

// traceEnv turns on the first-frame trace: LAB_TRACE_FIRSTFRAME=1 lab.
const traceEnv = "LAB_TRACE_FIRSTFRAME"

// frameTrace times the TUI start-up. main is when main() began, run when
// config and state were loaded and the program was about to start, and view
// when the running program first called View.
type frameTrace struct {
	main, run, view time.Time
}

// trace is nil unless traceEnv is set. It is the only start-up global and it
// stays in cmd/.
var trace *frameTrace

func startTrace(now time.Time) {
	if os.Getenv(traceEnv) == "1" {
		trace = &frameTrace{main: now}
	}
}

// report prints the trace. It runs after the TUI exits, so it does not draw
// over the screen.
func (t *frameTrace) report(w io.Writer) {
	if t == nil {
		return
	}
	if t.view.IsZero() {
		_, _ = fmt.Fprintln(w, "lab: first frame: View was never called")
		return
	}
	ms := func(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }
	_, _ = fmt.Fprintf(w, "lab: first frame %s after main (setup %s, program start to first View %s)\n",
		ms(t.view.Sub(t.main)), ms(t.run.Sub(t.main)), ms(t.view.Sub(t.run)))
}
