package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chanupen/devops-lab/internal/assist"
	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/state"
)

type fakeAssistant struct {
	alerts func(context.Context) ([]assist.Alert, error)
	run    func(context.Context, AssistRequest, func(assist.Progress)) (assist.Note, error)
}

func (f fakeAssistant) Alerts(ctx context.Context) ([]assist.Alert, error) { return f.alerts(ctx) }
func (f fakeAssistant) Run(ctx context.Context, req AssistRequest, progress func(assist.Progress)) (assist.Note, error) {
	return f.run(ctx, req, progress)
}

func assistantModel() Model {
	m := newModel(labState(2))
	m.screen = screenDrills
	m.d.st.ActiveDrill = &state.DrillRun{Drill: "oom", Phase: state.DrillRunning}
	m.opts.Assistant = fakeAssistant{
		alerts: func(context.Context) ([]assist.Alert, error) { panic("unexpected alert listing") },
		run: func(context.Context, AssistRequest, func(assist.Progress)) (assist.Note, error) {
			panic("the key loop invoked the runner")
		},
	}
	return m
}

func incidentNote() assist.Note {
	return assist.Note{
		WhatFired: []assist.Claim{{Text: "OOM alert fired", Evidence: []string{"E1"}}},
		Candidate: &assist.Claim{CauseID: "memory-limit", Text: "The limit is too low", Evidence: []string{"E2"}},
		Cause:     []assist.Claim{{Text: "The limit is too low", Evidence: []string{"E2"}}},
		Confirm:   []assist.Claim{{Text: "Inspect the memory limit", Evidence: []string{"E2"}}},
		Fix:       []assist.Claim{{Text: "Review the workload", Unsupported: true}},
		Evidence:  []assist.Evidence{{ID: "E2", Source: "pods", At: t0, Data: "OOMKilled at the memory limit"}},
		TraceID:   "example-trace",
	}
}

func TestAssistantPaneStates(t *testing.T) {
	for _, want := range []assistState{assistIdle, assistRunning, assistDone, assistAbstained, assistError, assistCancelled} {
		t.Run(want.String(), func(t *testing.T) {
			m := assistantModel()
			next, cmd := m.Update(key("a"))
			m = next.(Model)
			if cmd == nil || !m.assistant.open || m.assistant.state != assistRunning {
				t.Fatal("a did not schedule a background run")
			}
			ctx := m.assistant.ctx
			t.Cleanup(func() { m.cancelAssistant() })
			note := incidentNote()
			switch want {
			case assistIdle:
				m.assistant.picking = true
				next, _ = m.Update(assistAlertsMsg{id: m.assistant.id, alerts: []assist.Alert{{Name: "Example"}}})
				m = next.(Model)
			case assistDone, assistAbstained, assistError:
				var err error
				if want == assistAbstained {
					note.Candidate = nil
					note.Cause = []assist.Claim{{Text: "The root cause is not established."}}
				}
				if want == assistError {
					note.Incomplete = true
					err = errors.New("model unavailable")
				}
				next, _ = m.Update(assistMsg{id: m.assistant.id, result: &assistResult{note: note, err: err}})
				m = next.(Model)
			case assistCancelled:
				next, _ = m.Update(key("esc"))
				m = next.(Model)
				select {
				case <-ctx.Done():
				default:
					t.Fatal("esc did not cancel the runner context")
				}
				next, _ = m.Update(assistMsg{id: m.assistant.id, result: &assistResult{note: note}})
				m = next.(Model)
			}
			view := plain(m)
			if m.assistant.state != want || !strings.Contains(view, "hypothesis, needs human review") ||
				!strings.Contains(view, "Assistant: "+want.String()) {
				t.Fatalf("state %s, want %s:\n%s", m.assistant.state, want, view)
			}
			if want == assistDone {
				for _, text := range []string{"Rule candidate: memory-limit", "[E2]", "OOMKilled at the memory limit", "[unsupported]", "example-trace"} {
					if !strings.Contains(view, text) {
						t.Errorf("note lacks %q:\n%s", text, view)
					}
				}
			}
			if want == assistAbstained && !strings.Contains(view, "root cause is not established") {
				t.Fatal("abstention was not displayed")
			}
			if want != assistIdle && !strings.Contains(view, "$ lab assist drill oom --rules-first") {
				t.Fatal("equivalent CLI command was not logged")
			}
		})
	}
}

func TestAssistantAlertSelectionAndStaleMessages(t *testing.T) {
	m := assistantModel()
	m.screen = screenHome
	m.opts.Assistant = fakeAssistant{alerts: func(context.Context) ([]assist.Alert, error) {
		return []assist.Alert{
			{Name: "CrashLoop", Status: "firing", Labels: map[string]string{"namespace": "demo", "pod": "first"}},
			{Name: "CrashLoop", Status: "firing", Labels: map[string]string{"namespace": "demo", "pod": "second"}},
		}, nil
	}}
	next, cmd := m.Update(key("a"))
	m = next.(Model)
	oldID := m.assistant.id
	next, _ = m.Update(cmd())
	m = next.(Model)
	next, _ = m.Update(key("j"))
	m = next.(Model)
	next, cmd = m.Update(key("a"))
	m = next.(Model)
	t.Cleanup(func() { m.cancelAssistant() })
	if cmd == nil || m.assistant.command != "lab assist alert CrashLoop --rules-first --label namespace=demo --label pod=second" {
		t.Fatalf("selected alert command: %s", m.assistant.command)
	}
	next, _ = m.Update(assistAlertsMsg{id: oldID, err: errors.New("late error")})
	m = next.(Model)
	next, _ = m.Update(assistMsg{id: oldID, result: &assistResult{err: errors.New("late result")}})
	m = next.(Model)
	if m.assistant.state != assistRunning {
		t.Fatal("a stale message replaced the new run")
	}
	next, _ = m.Update(key("ctrl+c"))
	if next.(Model).assistant.state != assistCancelled {
		t.Fatal("quitting did not cancel the assistant")
	}
}

func TestAssistantKeepsFullCommandAndReviewLabel(t *testing.T) {
	m := assistantModel()
	m.opts.AssistFlags = []string{"--config", "/repo/lab.yaml", "--state", "/tmp/lab state.json", "--langfuse-ca", "/tmp/ca.pem"}
	next, _ := m.startAssistant(AssistRequest{Kind: "alert", Name: "Example", Labels: map[string]string{
		"description": strings.Repeat("\n", 30), "namespace": "demo",
	}})
	m = next.(Model)
	t.Cleanup(func() { m.cancelAssistant() })
	next, _ = m.Update(assistMsg{id: m.assistant.id, result: &assistResult{note: incidentNote()}})
	m = next.(Model)
	if got := m.assistant.text.slice(0, 1)[0]; got != "$ "+m.assistant.command {
		t.Fatalf("full command lost from completed note: %s", got)
	}
	if !strings.Contains(plain(m), "hypothesis, needs human review") {
		t.Fatal("a multiline alert label hid the review warning")
	}
}

func TestAssistantNoDrillMutationAndReadOnlyAccess(t *testing.T) {
	m := assistantModel()
	m.d.st.ActiveDrill = nil
	next, cmd := m.Update(key("a"))
	m = next.(Model)
	if cmd != nil || !strings.Contains(m.flash, "no drill is running") {
		t.Fatal("a must not start a drill")
	}
	m.d.st.ActiveDrill = &state.DrillRun{Drill: "oom", Phase: state.DrillRunning, Random: true}
	m.d.readOnly = "lab up (pid 42)"
	next, cmd = m.Update(key("a"))
	m = next.(Model)
	t.Cleanup(func() { m.cancelAssistant() })
	if cmd == nil || m.drills.busy != "" || m.confirm != nil {
		t.Fatal("read-only assistance was blocked or requested a mutation")
	}
}

func TestAssistantStreamsWithoutBlockingKeys(t *testing.T) {
	started := make(chan struct{})
	exited := make(chan struct{})
	m := assistantModel()
	m.opts.Assistant = fakeAssistant{run: func(ctx context.Context, req AssistRequest, progress func(assist.Progress)) (assist.Note, error) {
		defer close(exited)
		if req.Kind != "drill" || req.Name != "oom" || len(req.Labels) != 0 {
			return assist.Note{}, errors.New("unexpected input")
		}
		close(started)
		progress(assist.Progress{Stage: "collecting", Evidence: &assist.Evidence{ID: "E2", Source: "pods", Data: "OOMKilled"}})
		<-ctx.Done()
		return assist.Note{}, ctx.Err()
	}}
	next, cmd := m.Update(key("a"))
	m = next.(Model)
	batch := cmd().(tea.BatchMsg)
	runDone := make(chan struct{})
	go func() { batch[0](); close(runDone) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background run did not start")
	}
	msg := make(chan tea.Msg, 1)
	go func() { msg <- batch[1]() }()
	select {
	case progress := <-msg:
		next, _ = m.Update(progress)
		m = next.(Model)
	case <-time.After(time.Second):
		t.Fatal("progress did not stream")
	}
	if !strings.Contains(plain(m), "[E2] pods: OOMKilled") {
		t.Fatal("streamed evidence was not displayed")
	}
	start := time.Now()
	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	next, _ = m.Update(key("esc"))
	m = next.(Model)
	if time.Since(start) >= 100*time.Millisecond || m.assistant.state != assistCancelled {
		t.Fatal("resize/esc blocked on the model")
	}
	for _, done := range []chan struct{}{exited, runDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cancellation left a background command blocked")
		}
	}
}

func TestAssistantProgressIsThrottled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out := make(chan tea.Msg)
	api := fakeAssistant{run: func(ctx context.Context, _ AssistRequest, progress func(assist.Progress)) (assist.Note, error) {
		for range 120 {
			progress(assist.Progress{Stage: "reasoning"})
			select {
			case <-time.After(time.Millisecond):
			case <-ctx.Done():
				return assist.Note{}, ctx.Err()
			}
		}
		return incidentNote(), nil
	}}
	go streamAssistant(ctx, api, AssistRequest{}, 1, out, redrawEvery)
	var last time.Time
	messages := 0
	for {
		select {
		case msg := <-out:
			now := time.Now()
			if !last.IsZero() && now.Sub(last) < redrawEvery-5*time.Millisecond {
				t.Fatalf("updates only %s apart", now.Sub(last))
			}
			last = now
			messages++
			if msg.(assistMsg).result != nil {
				if messages < 2 {
					t.Fatal("progress was buffered until completion")
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("stream did not finish")
		}
	}
}

func TestAssistantRingAndTerminalSafety(t *testing.T) {
	m := assistantModel()
	next, _ := m.Update(key("a"))
	m = next.(Model)
	t.Cleanup(func() { m.cancelAssistant() })
	for i := range logLines + 10 {
		m.assistant.text.add(fmt.Sprintf("line %d", i))
	}
	m.assistant.text.add("\x1b]52;c;clipboard\x07\x1b[2Jlast\rline")
	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	next, _ = m.Update(key("G"))
	m = next.(Model)
	if m.assistant.text.len() != logLines || m.assistant.view.lines.len() > logLines {
		t.Fatal("assistant output exceeded the ring bound")
	}
	view := m.View()
	if strings.Contains(view, "clipboard") || strings.Contains(view, "\x1b[2J") || strings.Contains(view, "\r") ||
		!strings.Contains(view, "lastline") {
		t.Fatal("untrusted output reached the terminal unsanitized")
	}
}

func TestAssistantFirstFrameGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := state.NewStore(path).Update(func(st *state.State) error { *st = labState(state.MaxOps); return nil }); err != nil {
		t.Fatal(err)
	}
	api := fakeAssistant{
		alerts: func(context.Context) ([]assist.Alert, error) { panic("first frame queried alerts") },
		run: func(context.Context, AssistRequest, func(assist.Progress)) (assist.Note, error) {
			panic("first frame ran the assistant")
		},
	}
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("assistant=%t", enabled), func(t *testing.T) {
			start := time.Now()
			st, err := state.NewStore(path).Load()
			if err != nil {
				t.Fatal(err)
			}
			opts := Options{Config: config.Default(), State: st, Catalog: engine.Catalog()}
			if enabled {
				opts.Assistant = api
			}
			m := New(opts)
			view := m.View()
			elapsed := time.Since(start)
			t.Logf("cached first frame: %s", elapsed)
			if elapsed >= 100*time.Millisecond || view == "" || m.assistant.state != assistIdle {
				t.Fatalf("first-frame regression: %s", elapsed)
			}
		})
	}
}
