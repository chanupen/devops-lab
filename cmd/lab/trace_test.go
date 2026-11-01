package main

import (
	"strings"
	"testing"
	"time"
)

func TestFrameTraceReport(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		trace *frameTrace
		want  string
	}{
		{"off", nil, ""},
		{"no frame", &frameTrace{main: t0, run: t0.Add(time.Millisecond)}, "lab: first frame: View was never called\n"},
		{"timed", &frameTrace{main: t0, run: t0.Add(1100 * time.Microsecond), view: t0.Add(1400 * time.Microsecond)},
			"lab: first frame 1.4 ms after main (setup 1.1 ms, program start to first View 0.3 ms)\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			tt.trace.report(&b)
			if b.String() != tt.want {
				t.Errorf("got %q, want %q", b.String(), tt.want)
			}
		})
	}
}

func TestStartTrace(t *testing.T) {
	t.Cleanup(func() { trace = nil })
	for _, v := range []string{"", "0", "1"} {
		t.Setenv(traceEnv, v)
		trace = nil
		startTrace(time.Now())
		if got := trace != nil; got != (v == "1") {
			t.Errorf("%s=%q: trace on %v", traceEnv, v, got)
		}
	}
}
