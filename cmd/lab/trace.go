package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/chanupen/devops-lab/cmd/lab/internal/starttime"
)

// traceEnv turns on the first-frame trace: LAB_TRACE_FIRSTFRAME=1 lab.
const traceEnv = "LAB_TRACE_FIRSTFRAME"

// frameTrace times the TUI start-up. init is the earliest package init lab
// can stamp (see package starttime), main when main() began, run when config and
// state were loaded and the program was about to start, and view when the
// first View the running program asked for returned.
type frameTrace struct {
	init, main, run, view time.Time
}

// trace is nil unless traceEnv is set. It is the only start-up global and it
// stays in cmd/.
var trace *frameTrace

func startTrace(now time.Time) {
	if os.Getenv(traceEnv) == "1" {
		trace = &frameTrace{init: starttime.At(), main: now}
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
	if !t.init.IsZero() {
		_, _ = fmt.Fprintf(w, "lab: first frame %s after package init began (init %s)\n",
			ms(t.view.Sub(t.init)), ms(t.main.Sub(t.init)))
	}
	_, _ = fmt.Fprintf(w, "lab: first frame %s after main (setup %s, program start to first View %s)\n",
		ms(t.view.Sub(t.main)), ms(t.run.Sub(t.main)), ms(t.view.Sub(t.run)))
}
