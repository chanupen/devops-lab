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
	for _, want := range []string{"a random one", "running 4m0s, 26m0s left", "the alert: fired after 1m15s",
		"detect: 3m0s", "hints: 1 of 2", randomPage} {
		if !strings.Contains(got, want) {
			t.Errorf("status lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "dns") || strings.Contains(got, "CoreDNS") || strings.Contains(got, d.Title) {
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

// The list's footer names a real drill and, only when a drill needs
// stacks that are down, those stacks.
func TestDrillListFooter(t *testing.T) {
	all := []drills.Drill{{ID: "bad-deploy", Stacks: []string{"sample-app"}}, {ID: "slow-db", Stacks: []string{"sample-db", "obs"}}}
	st := state.New()
	st.Stacks["sample-app"] = state.StackState{Status: state.Applied}
	st.Stacks["obs"] = state.StackState{Status: state.Applied}
	got := drillListFooter(all, st)
	if !strings.Contains(got, "○ needs stacks that are not up: lab up --stacks sample-db\n") ||
		!strings.Contains(got, "lab drills start bad-deploy, or lab drills start --random") {
		t.Errorf("footer with a stack down:\n%s", got)
	}
	st.Stacks["sample-db"] = state.StackState{Status: state.Applied}
	if got := drillListFooter(all, st); strings.Contains(got, "○") || !strings.Contains(got, "lab drills start bad-deploy,") {
		t.Errorf("footer with every stack up:\n%s", got)
	}
}

// History marks each ack right or wrong; reset deletes one drill's runs
// or all of them.
func TestDrillHistoryAcksAndReset(t *testing.T) {
	t0 := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	runs := []state.DrillRun{
		{Drill: "dns", Outcome: state.DrillRecovered, Started: t0, Acked: t0.Add(3 * time.Minute),
			Acks: []state.DrillAck{{Cause: "netpol"}, {Cause: "coredns-down", Right: true}}},
		{Drill: "oom", Outcome: state.DrillAborted, Started: t0},
	}
	var b strings.Builder
	if err := writeDrillHistory(&b, runs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "✕ netpol, ● coredns-down") {
		t.Errorf("history:\n%s", b.String())
	}
	st := state.State{Drills: runs}
	if n := st.ResetDrills("dns"); n != 1 || len(st.Drills) != 1 || st.Drills[0].Drill != "oom" {
		t.Errorf("reset dns: %d, %v", n, st.Drills)
	}
	if n := st.ResetDrills(""); n != 1 || st.Drills != nil {
		t.Errorf("reset all: %d, %v", n, st.Drills)
	}
}
