package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/drills"
	"github.com/chanupen/devops-lab/internal/state"
)

// A random drill does not say which it is while it runs; its times show.
func TestDrillStatus(t *testing.T) {
	start := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	d := drills.Drill{ID: "dns", Title: "Service discovery fails", Hints: []string{"a", "b"}, Page: "The pager goes off."}
	r := state.DrillRun{Drill: "dns", Random: true, Phase: state.DrillRunning, Started: start, Deadline: start.Add(30 * time.Minute),
		Alert: "CoreDNSDown", AlertFired: start.Add(75 * time.Second), Acked: start.Add(3 * time.Minute), Hints: 1}
	got := drillStatus(d, r, start.Add(4*time.Minute))
	for _, want := range []string{"a random one", "running 4m0s, 26m0s left", "alert CoreDNSDown: fired after 1m15s",
		"detect: 3m0s", "hints: 1 of 2", randomPage} {
		if !strings.Contains(got, want) {
			t.Errorf("status lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "dns") || strings.Contains(got, d.Title) {
		t.Errorf("a random drill's status gives it away:\n%s", got)
	}
	r.Random, r.Phase, r.Error = false, state.DrillRestoring, "deployments/coredns: spec differ"
	if got := drillStatus(d, r, start); !strings.Contains(got, "lab drills abort tries again") || !strings.Contains(got, "spec differ") {
		t.Errorf("failed restore:\n%s", got)
	}
}

func TestDrillHistory(t *testing.T) {
	start := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	runs := []state.DrillRun{
		{Drill: "oom", Started: start, Alert: "KubeContainerOOMKilled", AlertFired: start.Add(2 * time.Minute),
			Acked: start.Add(3 * time.Minute), Recovered: start.Add(9 * time.Minute), Outcome: state.DrillRecovered},
		{Drill: "oom", Random: true, Started: start, Alert: "KubeContainerOOMKilled", Acked: start.Add(time.Minute),
			Recovered: start.Add(5 * time.Minute), Outcome: state.DrillRecovered, Hints: 2},
		{Drill: "dns", Started: start, Alert: "CoreDNSDown", Outcome: state.DrillAborted},
	}
	var b bytes.Buffer
	if err := writeDrillHistory(&b, runs); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"oom (random)", "did not fire", "aborted"} {
		if !strings.Contains(out, want) {
			t.Errorf("history lacks %q:\n%s", want, out)
		}
	}
	best := map[string]string{}
	_, tail, _ := strings.Cut(out, "Best")
	for _, l := range strings.Split(tail, "\n") {
		if f := strings.Fields(l); len(f) == 6 {
			best[f[0]] = strings.Join(f[1:], " ")
		}
	}
	if best["oom"] != "2 2 2m0s 1m0s 5m0s" || best["dns"] != "1 0 — — —" {
		t.Errorf("best = %v", best)
	}
}
