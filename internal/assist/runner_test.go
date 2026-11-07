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
		Review:    "verified diagnosis; no review needed",
		WhatFired: []Claim{{Text: "alert fired", Evidence: []string{"E1"}}},
		Cause:     []Claim{{Text: "unknown"}},
		Confirm:   []Claim{{Text: "inspect pod", Evidence: []string{"E2"}}},
		Fix:       []Claim{{Text: "investigate", Evidence: []string{"E2"}}},
	}}}}
	tracer := &fakeTracer{}
	var progress []Progress
	note, err := (Runner{Tools: tools, Model: model, Tracer: tracer,
		Progress: func(p Progress) { progress = append(progress, p) }}).Run(context.Background(),
		Alert{Name: "Example", Status: "firing", Labels: map[string]string{"namespace": "demo", "app": "frontend"}})
	if err != nil {
		t.Fatal(err)
	}

	if note.Review != HypothesisLabel || !note.Cause[0].Unsupported || note.WhatFired[0].Unsupported || note.TraceID != "trace-id" || len(note.Evidence) != 7 {
		t.Fatalf("note = %+v", note)
	}
	b, _ := json.Marshal(struct {
		Inputs   [][]Evidence
		Progress []Progress
	}{model.inputs, progress})
	for _, secret := range []string{"inline-secret", "helm-secret", "log-secret"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("secret %q reached model: %s", secret, b)
		}
	}
	if tracer.evidence != 7 || tracer.calls != 0 || tracer.incomplete {
		t.Fatalf("trace = %+v", tracer)
	}
}

func TestRunnerProgressAndCandidate(t *testing.T) {
	fixtures, err := LoadEvalFixtures("testdata/eval")
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		if fixture.ID != "oom" {
			continue
		}
		model := &fakeModel{decisions: []Decision{{Note: &Note{
			Candidate: &Claim{Text: "model invented a rule"},
			WhatFired: []Claim{{Text: "Alert fired", Evidence: []string{"E1"}}},
			Cause:     []Claim{{Text: "not established"}},
			Confirm:   []Claim{{Text: "Inspect the pod", Evidence: []string{"E2"}}},
			Fix:       []Claim{{Text: "Review the limit", Evidence: []string{"E2"}}},
		}}}}
		var updates []Progress
		note, err := (Runner{Tools: FixtureTools(fixture.Tools), Model: model, Tracer: &fakeTracer{}, RulesFirst: true,
			Progress: func(p Progress) { updates = append(updates, p) }}).Run(context.Background(), fixture.Alert)
		if err != nil {
			t.Fatal(err)
		}
		if note.Candidate == nil || note.Candidate.CauseID != "memory-limit" || note.Abstained() {
			t.Fatalf("candidate = %+v", note.Candidate)
		}
		evidence, candidate, reasoning, tracing := 0, false, false, false
		for _, p := range updates {
			if p.Evidence != nil {
				evidence++
				if len(p.Evidence.Data) > evidenceLimit+len(" [truncated]") {
					t.Fatal("progress escaped evidence bounds")
				}
			}
			candidate = candidate || p.Candidate != nil && p.Candidate.CauseID == "memory-limit"
			reasoning = reasoning || p.Stage == "reasoning"
			tracing = tracing || p.Stage == "tracing"
		}
		if evidence != len(note.Evidence) || !candidate || !reasoning || !tracing {
			t.Fatalf("incomplete progress: %d evidence, candidate %t, reasoning %t, tracing %t", evidence, candidate, reasoning, tracing)
		}
		return
	}
	t.Fatal("OOM fixture missing")
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

func TestRedactPreservesImageReferences(t *testing.T) {
	for _, image := range []string{
		"lab-zot:5000/lab/podinfo:6.15.1",
		"cert-manager:1",
		"ghcr.io/example/app@sha256:" + strings.Repeat("a", 64),
	} {
		input, _ := json.Marshal(map[string]string{"image": image})
		if got := Redact(string(input)); !strings.Contains(got, image) {
			t.Errorf("image lost: %s", got)
		}
	}
	if got := Redact(`{"image":"PASSWORD=inline-secret","env":[{"value":"env-secret"}]}`); strings.Contains(got, "inline-secret") || strings.Contains(got, "env-secret") {
		t.Fatalf("invalid image or env leaked: %s", got)
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

func TestLivePodSignalsFitEvidenceBound(t *testing.T) {
	input := `{"name":"frontend-123","namespace":"demo","app":"frontend","node":"worker","containers":[{"name":"app","image":"lab-zot:5000/lab/podinfo:6.15.1","resources":{"limits":{"memory":"64Mi"}}}],"containerStatuses":[{"name":"app","image":"lab-zot:5000/lab/podinfo:6.15.1","restartCount":6,"lastState":{"terminated":{"reason":"Error","exitCode":1,"containerID":"` + strings.Repeat("x", 300) + `"}},"state":{"waiting":{"reason":"CrashLoopBackOff","message":"` + strings.Repeat("x", 700) + `"}}}]}`
	got := focusPodEvidence(Redact(input))
	if len(got) > evidenceLimit || !json.Valid([]byte(got)) {
		t.Fatalf("focused pod exceeds the bound: %s", got)
	}
	for _, want := range []string{"lab-zot:5000/lab/podinfo:6.15.1", "CrashLoopBackOff", `"restartCount":6`, `"exitCode":1`} {
		if !strings.Contains(got, want) {
			t.Errorf("signal %q missing in %s", want, got)
		}
	}
}

func TestFindingsPreserveHistoryAndNodeConditions(t *testing.T) {
	got := Redact(deterministicFindings([]Evidence{
		{ID: "E6", Source: "argocd_history", Data: `[{"revision":"new-release"}]`},
		{ID: "E7", Source: "nodes", Data: `{"conditions":[{"reason":"KubeletNotReady"}]}`},
	}))
	for _, want := range []string{"new-release", "KubeletNotReady", "E6", "E7"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in findings: %s", want, got)
		}
	}
}

func TestRuleCandidateChecksLaterObservations(t *testing.T) {
	candidate := ruleCandidate([]Evidence{
		{ID: "E2", Source: "pods", Data: `{"restartCount":0}`},
		{ID: "E8", Source: "pods", Data: `{"reason":"OOMKilled","exitCode":137}`},
	})
	if candidate == nil || candidate.CauseID != "memory-limit" ||
		len(candidate.Evidence) != 1 || candidate.Evidence[0] != "E8" {
		t.Fatalf("candidate = %+v", candidate)
	}
}

func TestReleaseRuleRequiresCorroboration(t *testing.T) {
	at := time.Date(2026, 11, 7, 9, 10, 0, 0, time.UTC)
	pod := `{"containerStatuses":[{"restartCount":6,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}`
	for _, tt := range []struct {
		name, history, events string
		want                  bool
	}{
		{"crash only", "", "", false},
		{"recent revision", `[{"id":1,"revision":"old"},{"id":2,"revision":"new","deployedAt":"2026-11-07T09:05:00Z"}]`, "", true},
		{"recent image override", `[{"id":1,"revision":"6.15.0"},{"id":2,"revision":"6.15.0","image":"lab-zot:5000/lab/podinfo:6.15.1","deployedAt":"2026-11-07T09:05:00Z"}]`, "", true},
		{"unchanged revision", `[{"id":1,"revision":"same"},{"id":2,"revision":"same","deployedAt":"2026-11-07T09:05:00Z"}]`, "", false},
		{"old revision", `[{"id":1,"revision":"old"},{"id":2,"revision":"new","deployedAt":"2026-11-07T08:05:00Z"}]`, "", false},
		{"image update event", "", `{"items":[{"message":"container exited after image update"}]}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			evidence := []Evidence{
				{ID: "E2", Source: "pods", At: at, Data: pod},
				{ID: "E3", Source: "events", At: at, Data: tt.events},
				{ID: "E6", Source: "argocd_history", At: at, Data: tt.history},
			}
			candidate := ruleCandidate(evidence)
			if (candidate != nil) != tt.want || (candidate != nil && (candidate.CauseID != "bad-release" || len(candidate.Evidence) != 2)) {
				t.Fatalf("candidate = %+v", candidate)
			}
		})
	}
}

type laterPodTools struct{ pods int }

func (t *laterPodTools) Call(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
	if name == "pods" {
		t.pods++
		if t.pods > 1 {
			return json.RawMessage(`{"reason":"OOMKilled","exitCode":137}`), nil
		}
	}
	return json.RawMessage(`{}`), nil
}

func TestRunnerUsesCauseFromExtraToolCall(t *testing.T) {
	call := Decision{}
	call.Tool = &struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"arguments"`
	}{"pods", json.RawMessage(`{"namespace":"demo"}`)}
	model := &fakeModel{decisions: []Decision{call, {Note: &Note{
		WhatFired: []Claim{{Text: "fired", Evidence: []string{"E1"}}},
		Cause:     []Claim{{Text: "The root cause is not established."}},
		Confirm:   []Claim{{Text: "inspect"}},
		Fix:       []Claim{{Text: "investigate"}},
	}}}}
	note, err := (Runner{Tools: &laterPodTools{}, Model: model, Tracer: &fakeTracer{}, RulesFirst: true}).
		Run(context.Background(), Alert{Name: "Example", Labels: map[string]string{"namespace": "demo", "app": "api"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(note.Cause) != 1 || note.Cause[0].CauseID != "memory-limit" ||
		len(note.Cause[0].Evidence) != 1 || note.Cause[0].Evidence[0] != "E8" {
		t.Fatalf("cause = %+v", note.Cause)
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
