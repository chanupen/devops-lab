package assist

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeTools struct {
	calls []string
	data  string
}

func (f *fakeTools) Call(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
	f.calls = append(f.calls, name)
	return json.RawMessage(f.data), nil
}

type fakeModel struct {
	decisions []Decision
	inputs    [][]Evidence
}

func (f *fakeModel) Decide(_ context.Context, _ Alert, evidence []Evidence) (Decision, error) {
	f.inputs = append(f.inputs, append([]Evidence(nil), evidence...))
	if len(f.decisions) == 0 {
		return Decision{}, errors.New("no decision")
	}
	d := f.decisions[0]
	f.decisions = f.decisions[1:]
	return d, nil
}

type fakeTracer struct {
	calls, evidence int
	incomplete      bool
}

func (f *fakeTracer) Trace(_ context.Context, _, _ time.Time, evidence []ToolObservation, calls int, incomplete bool) (string, error) {
	f.calls, f.evidence, f.incomplete = calls, len(evidence), incomplete
	return "trace-id", nil
}

func TestRunnerEvidenceAndCitations(t *testing.T) {
	tools := &fakeTools{data: `{"env":[{"name":"PASSWORD","value":"inline-secret"}],"helm":{"parameters":[{"name":"password","value":"helm-secret"}]},"line":"TOKEN=log-secret"}`}
	model := &fakeModel{decisions: []Decision{{Note: &Note{
		WhatFired: []Claim{{Text: "alert fired", Evidence: []string{"E1"}}},
		Cause:     []Claim{{Text: "unknown"}},
		Confirm:   []Claim{{Text: "inspect pod", Evidence: []string{"E2"}}},
		Fix:       []Claim{{Text: "investigate", Evidence: []string{"E2"}}},
	}}}}
	tracer := &fakeTracer{}
	note, err := (Runner{Tools: tools, Model: model, Tracer: tracer}).Run(context.Background(),
		Alert{Name: "Example", Status: "firing", Labels: map[string]string{"namespace": "demo", "app": "frontend"}})
	if err != nil {
		t.Fatal(err)
	}
	if !note.Cause[0].Unsupported || note.WhatFired[0].Unsupported || note.TraceID != "trace-id" || len(note.Evidence) != 7 {
		t.Fatalf("note = %+v", note)
	}
	b, _ := json.Marshal(model.inputs)
	for _, secret := range []string{"inline-secret", "helm-secret", "log-secret"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("secret %q reached model: %s", secret, b)
		}
	}
	if tracer.evidence != 7 || tracer.calls != 0 || tracer.incomplete {
		t.Fatalf("trace = %+v", tracer)
	}
}

func TestRunnerRejectsUnknownEvidence(t *testing.T) {
	tools := &fakeTools{data: `{}`}
	model := &fakeModel{decisions: []Decision{{Note: &Note{
		WhatFired: []Claim{{Text: "fired", Evidence: []string{"E999"}}},
		Cause:     []Claim{{Text: "unknown"}},
		Confirm:   []Claim{{Text: "inspect"}},
		Fix:       []Claim{{Text: "review"}},
	}}}}
	trace := &fakeTracer{}
	note, err := (Runner{Tools: tools, Model: model, Tracer: trace}).Run(context.Background(), Alert{Name: "A"})
	if err == nil || !note.Incomplete || !trace.incomplete || len(note.Evidence) == 0 {
		t.Fatalf("note = %+v, error %v", note, err)
	}
}

func TestRunnerPodConfirmation(t *testing.T) {
	tests := []struct {
		name, namespace, pod, want string
	}{
		{"valid labels", "demo", "frontend-123", "kubectl -n demo describe pod frontend-123"},
		{"invalid pod", "demo", "bad;name", "inspect the alert"},
		{"missing namespace", "", "frontend-123", "inspect the alert"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &fakeModel{decisions: []Decision{{Note: &Note{
				WhatFired: []Claim{{Text: "fired", Evidence: []string{"E1"}}},
				Cause:     []Claim{{Text: "unknown"}},
				Confirm:   []Claim{{Text: "inspect the alert"}},
				Fix:       []Claim{{Text: "investigate"}},
			}}}}
			note, err := (Runner{Tools: &fakeTools{data: `{}`}, Model: model, Tracer: &fakeTracer{}}).
				Run(context.Background(), Alert{Name: "A", Labels: map[string]string{"namespace": tt.namespace, "pod": tt.pod}})
			if err != nil {
				t.Fatal(err)
			}
			if len(note.Confirm) != 1 || note.Confirm[0].Text != tt.want {
				t.Fatalf("confirmation = %+v", note.Confirm)
			}
			if tt.name == "valid labels" && (note.Confirm[0].Unsupported || len(note.Confirm[0].Evidence) != 1 || note.Confirm[0].Evidence[0] != "E1") {
				t.Fatalf("confirmation citation = %+v", note.Confirm[0])
			}
		})
	}
}

func TestRunnerBoundsExtraCalls(t *testing.T) {
	var decisions []Decision
	for range ExtraCallLimit + 1 {
		d := Decision{}
		d.Tool = &struct {
			Name string          `json:"name"`
			Args json.RawMessage `json:"arguments"`
		}{"pods", json.RawMessage(`{"namespace":"demo"}`)}
		decisions = append(decisions, d)
	}
	trace := &fakeTracer{}
	note, err := (Runner{Tools: &fakeTools{data: `{}`}, Model: &fakeModel{decisions: decisions}, Tracer: trace}).
		Run(context.Background(), Alert{Name: "A"})
	if err == nil || !note.Incomplete || trace.calls != ExtraCallLimit || !trace.incomplete {
		t.Fatalf("note = %+v, trace %+v, error %v", note, trace, err)
	}
}

func TestRunnerCancelledStillAttemptsTrace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	trace := &fakeTracer{}
	note, err := (Runner{Tools: &fakeTools{data: `{}`}, Model: &fakeModel{}, Tracer: trace}).
		Run(ctx, Alert{Name: "A"})
	if !errors.Is(err, context.Canceled) || !note.Incomplete || !trace.incomplete || note.TraceID == "" {
		t.Fatalf("note = %+v, trace %+v, error %v", note, trace, err)
	}
}

func TestRedact(t *testing.T) {
	tests := []struct{ input, secret string }{
		{`{"env":[{"name":"X","value":"abc"}]}`, "abc"},
		{`{"helm":{"parameters":[{"name":"password","value":"abc"}]}}`, "abc"},
		{`{"line":"TOKEN=abc"}`, "abc"},
		{`helm upgrade --set password=abc`, "abc"},
		{`{"line":"{\"env\":[{\"value\":\"abc\"}]}"}`, "abc"},
		{"env:\n  - name: PASSWORD\n    value: abc", "abc"},
	}

	for _, tt := range tests {
		if out := Redact(tt.input); strings.Contains(out, tt.secret) {
			t.Errorf("redaction %q = %q", tt.input, out)
		}
	}
}

func TestRedactPreservesLokiValues(t *testing.T) {
	out := Redact(`{"data":{"result":[{"values":[["123","server stopped"]]}]}}`)
	if !strings.Contains(out, "server stopped") {
		t.Fatalf("Loki log lost: %s", out)
	}
}

func TestRedactPreservesTimestamps(t *testing.T) {
	const timestamp = "2026-11-06T10:15:30Z"
	if got := Redact(`{"at":"` + timestamp + `"}`); !strings.Contains(got, timestamp) {
		t.Fatalf("timestamp lost: %s", got)
	}
}

func TestFocusPodEvidence(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
		drop  []string
	}{
		{"termination", `{"name":"p","conditions":[{"message":"` + strings.Repeat("x", 700) + `"}],"containerStatuses":[{"name":"app","lastState":{"terminated":{"reason":"OOMKilled","exitCode":137}},"imageID":"unneeded","restartCount":2}]}`, []string{"OOMKilled", "137", "restartCount"}, []string{"conditions", "imageID"}},
		{"pending", `{"name":"p","conditions":[{"reason":"Unschedulable"}]}`, []string{"Unschedulable"}, nil},
		{"list", `{"items":[{"name":"p","conditions":[{"reason":"Ready"}],"containerStatuses":[{"name":"app","state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}],"truncated":true}`, []string{"CrashLoopBackOff", `"truncated":true`}, []string{"conditions"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := focusPodEvidence(tt.input)
			for _, s := range tt.want {
				if !strings.Contains(got, s) {
					t.Errorf("missing %q in %s", s, got)
				}
			}
			for _, s := range tt.drop {
				if strings.Contains(got, s) {
					t.Errorf("unexpected %q in %s", s, got)
				}
			}
		})
	}
}

func TestRunnerRemovesTraceKeys(t *testing.T) {
	tools := &fakeTools{data: `{"message":"secret-fixture"}`}
	model := &fakeModel{decisions: []Decision{{Note: &Note{
		WhatFired: []Claim{{Text: "secret-fixture", Evidence: []string{"E1"}}},
		Cause:     []Claim{{Text: "unknown"}},
		Confirm:   []Claim{{Text: "inspect"}},
		Fix:       []Claim{{Text: "investigate"}},
	}}}}
	note, err := (Runner{Tools: tools, Model: model, Tracer: &fakeTracer{}, Sensitive: []string{"secret-fixture"}}).
		Run(context.Background(), Alert{Name: "A", Labels: map[string]string{"namespace": "demo", "message": "secret-fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	output, _ := json.Marshal(struct {
		Note   Note
		Inputs [][]Evidence
	}{note, model.inputs})
	if strings.Contains(string(output), "secret-fixture") {
		t.Fatalf("key leaked: %s", output)
	}
}
