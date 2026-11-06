package assist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Limits on a single assistant run.
const (
	RunTimeout     = 180 * time.Second
	ExtraCallLimit = 8
	evidenceLimit  = 600
)

// Alert is the safe input shared by the CLI and the webhook.
type Alert struct {
	Name   string            `json:"name"`
	Status string            `json:"status"`
	Labels map[string]string `json:"labels"`
}

// Evidence is a bounded, redacted observation from one source.
type Evidence struct {
	ID     string    `json:"id"`
	Source string    `json:"source"`
	At     time.Time `json:"at"`
	Data   string    `json:"data"`
}

// Claim is a statement with references to observations.
type Claim struct {
	Text        string   `json:"text"`
	Evidence    []string `json:"evidence"`
	Unsupported bool     `json:"unsupported"`
}

// Note is the incident note. Commands are suggestions, never executed.
type Note struct {
	WhatFired  []Claim    `json:"what_fired"`
	Evidence   []Evidence `json:"evidence"`
	Cause      []Claim    `json:"likely_cause"`
	Confirm    []Claim    `json:"commands_to_confirm"`
	Fix        []Claim    `json:"fix"`
	Incomplete bool       `json:"incomplete"`
	Error      string     `json:"error,omitempty"`
	TraceID    string     `json:"trace_id,omitempty"`
}

// Decision is the model's structured next step.
type Decision struct {
	Tool *struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"arguments"`
	} `json:"tool,omitempty"`
	Note *Note `json:"note,omitempty"`
}

// Model generates one validated decision from the evidence available so far.
type Model interface {
	Decide(context.Context, Alert, []Evidence) (Decision, error)
}

// Tracer records run metadata without evidence, prompts, notes or credentials.
type Tracer interface {
	Trace(context.Context, time.Time, time.Time, []ToolObservation, int, bool) (string, error)
}

// ToolObservation contains only metadata safe to include in a trace.
type ToolObservation struct {
	EvidenceID string `json:"evidence_id"`
	Source     string `json:"source"`
	DurationMS int64  `json:"duration_ms"`
	Failed     bool   `json:"failed"`
}

// Runner orchestrates the model and fixed read-only tools.
type Runner struct {
	Tools  Tools
	Model  Model
	Tracer Tracer
	Now    func() time.Time
	// Sensitive values are removed even if they appear in upstream output.
	Sensitive []string
}

func (r Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// Run gathers the initial evidence, then allows at most eight extra calls.
// Errors return an incomplete note alongside a non-nil error.
func (r Runner) Run(ctx context.Context, alert Alert) (note Note, runErr error) {
	started := r.now()
	calls := 0
	var observations []ToolObservation
	ctx, cancel := context.WithTimeout(ctx, RunTimeout)
	defer cancel()
	defer func() {
		if runErr != nil {
			note.Incomplete = true
			note.Error = "assistant run incomplete: " + runErr.Error()
		}
		if r.Tracer == nil {
			note.Incomplete = true
			runErr = errors.Join(runErr, errors.New("langfuse tracer is unavailable"))
			note.Error = "assistant run incomplete: Langfuse tracer is unavailable"
			return
		}
		traceCtx, traceCancel := context.WithTimeout(ctx, 10*time.Second)
		defer traceCancel()
		id, err := r.Tracer.Trace(traceCtx, started, r.now(), observations, calls, note.Incomplete)
		if err != nil {
			note.Incomplete = true
			runErr = errors.Join(runErr, fmt.Errorf("langfuse trace failed: %w", err))
			note.Error = "assistant run incomplete: Langfuse trace failed"
		} else {
			note.TraceID = id
		}
	}()
	if r.Tools == nil || r.Model == nil || alert.Name == "" {
		return note, errors.New("alert, tools and model are required")
	}
	add := func(source, data string, started time.Time, failed bool) {
		for _, secret := range r.Sensitive {
			if secret != "" {
				data = strings.ReplaceAll(data, secret, "[redacted]")
			}
		}
		data = Redact(data)
		if source == "pods" {
			data = focusPodEvidence(data)
		}
		if len(data) > evidenceLimit {
			data = data[:evidenceLimit] + " [truncated]"
		}
		note.Evidence = append(note.Evidence, Evidence{
			ID: "E" + strconv.Itoa(len(note.Evidence)+1), Source: source, At: r.now(), Data: data,
		})
		observations = append(observations, ToolObservation{
			EvidenceID: note.Evidence[len(note.Evidence)-1].ID, Source: source,
			DurationMS: r.now().Sub(started).Milliseconds(), Failed: failed,
		})
	}
	alertJSON, err := json.Marshal(alert)
	if err != nil {
		return note, err
	}
	safeJSON := string(alertJSON)
	for _, secret := range r.Sensitive {
		if secret != "" {
			safeJSON = strings.ReplaceAll(safeJSON, secret, "[redacted]")
		}
	}
	safeAlert := Alert{}
	if err := json.Unmarshal([]byte(Redact(safeJSON)), &safeAlert); err != nil {
		return note, errors.New("invalid redacted alert")
	}
	add("alert", string(alertJSON), r.now(), false)
	for _, call := range recipe(alert, started) {
		if ctx.Err() != nil {
			return note, ctx.Err()
		}
		if call.err != "" {
			add(call.name, call.err, r.now(), true)
			continue
		}
		callStarted := r.now()
		data, err := r.Tools.Call(ctx, call.name, call.args)
		if err != nil {
			add(call.name, "collection failed: "+safeError(err), callStarted, true)
		} else {
			add(call.name, string(data), callStarted, false)
		}
	}
	for {
		if ctx.Err() != nil {
			return note, ctx.Err()
		}
		decision, err := r.Model.Decide(ctx, safeAlert, note.Evidence)
		if err != nil {
			return note, fmt.Errorf("model failed: %s", safeError(err))
		}
		if (decision.Tool == nil) == (decision.Note == nil) {
			return note, errors.New("model returned an invalid decision")
		}
		if decision.Note != nil {
			result := *decision.Note
			result.Evidence = note.Evidence
			result.Error, result.TraceID, result.Incomplete = "", "", false
			if ns, pod := safeAlert.Labels["namespace"], safeAlert.Labels["pod"]; validNamespace(ns) && validName(pod) {
				result.Confirm = []Claim{{
					Text:     "kubectl -n " + ns + " describe pod " + pod,
					Evidence: []string{"E1"},
				}}
			}
			if err := validateNote(&result, r.Sensitive); err != nil {
				return note, err
			}
			note = result
			return note, nil
		}
		if calls >= ExtraCallLimit {
			return note, errors.New("additional tool-call budget exhausted")
		}
		calls++
		switch decision.Tool.Name {
		case "events", "pods", "pod_describe", "loki_logs", "prometheus_query", "argocd_history":
		default:
			return note, errors.New("model requested an unknown tool")
		}
		callStarted := r.now()
		data, err := r.Tools.Call(ctx, decision.Tool.Name, decision.Tool.Args)
		if err != nil {
			add(decision.Tool.Name, "tool failed: "+safeError(err), callStarted, true)
		} else {
			add(decision.Tool.Name, string(data), callStarted, false)
		}
	}
}

func focusPodEvidence(data string) string {
	var object map[string]any
	if json.Unmarshal([]byte(data), &object) != nil {
		return data
	}
	focus := func(pod map[string]any) {
		statuses, ok := pod["containerStatuses"].([]any)
		if !ok || len(statuses) == 0 {
			return
		}
		delete(pod, "conditions")
		for _, raw := range statuses {
			status, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			for key := range status {
				switch key {
				case "name", "ready", "restartCount", "lastState", "state":
				default:
					delete(status, key)
				}
			}
		}
	}
	if items, ok := object["items"].([]any); ok {
		for _, item := range items {
			if pod, ok := item.(map[string]any); ok {
				focus(pod)
			}
		}
	} else {
		focus(object)
	}
	b, err := json.Marshal(object)
	if err != nil {
		return data
	}
	return string(b)
}

type recipeCall struct {
	name string
	args json.RawMessage
	err  string
}

func recipe(alert Alert, now time.Time) []recipeCall {
	var calls []recipeCall
	appendCall := func(name string, args map[string]string) {
		b, _ := json.Marshal(args)
		calls = append(calls, recipeCall{name: name, args: b})
	}
	ns, pod := alert.Labels["namespace"], alert.Labels["pod"]
	if !validNamespace(ns) {
		for _, name := range []string{"pods", "events", "loki_logs"} {
			calls = append(calls, recipeCall{name: name, err: "namespace missing or invalid"})
		}
	} else {
		podArgs := map[string]string{"namespace": ns}
		if validName(pod) {
			podArgs["pod"] = pod
		}
		appendCall("pods", podArgs)
		appendCall("events", podArgs)
		appendCall("loki_logs", map[string]string{
			"query": "{namespace=" + strconv.Quote(ns) + "}",
			"start": now.Add(-15 * time.Minute).Format(time.RFC3339Nano),
			"end":   now.Format(time.RFC3339Nano),
		})
	}
	query := `ALERTS{alertname=` + strconv.Quote(alert.Name) + `}`
	appendCall("prometheus_query", map[string]string{"query": query, "end": now.Format(time.RFC3339Nano)})
	app := alert.Labels["app"]
	if !validName(app) {
		calls = append(calls, recipeCall{name: "argocd_history", err: "application label missing or invalid"})
	} else {
		appendCall("argocd_history", map[string]string{"app": app})
	}
	return calls
}

func validateNote(note *Note, sensitive []string) error {
	valid := make(map[string]bool, len(note.Evidence))
	for _, e := range note.Evidence {
		valid[e.ID] = true
	}
	for _, claims := range [][]Claim{note.WhatFired, note.Cause, note.Confirm, note.Fix} {
		for i := range claims {
			if strings.TrimSpace(claims[i].Text) == "" {
				return errors.New("model returned an empty claim")
			}
			claims[i].Text = Redact(claims[i].Text)
			for _, secret := range sensitive {
				if secret != "" {
					claims[i].Text = strings.ReplaceAll(claims[i].Text, secret, "[redacted]")
				}
			}
			for _, id := range claims[i].Evidence {
				if !valid[id] {
					return errors.New("model cited an unknown evidence ID")
				}
			}
			if len(claims[i].Evidence) == 0 {
				claims[i].Unsupported = true
			}
		}
	}
	if len(note.WhatFired) == 0 || len(note.Cause) == 0 || len(note.Confirm) == 0 || len(note.Fix) == 0 {
		return errors.New("model omitted an incident note section")
	}
	return nil
}

func safeError(err error) string {
	// Do not put an upstream response, credential or URL in evidence.
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline exceeded"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "request failed"
	}
}

// Labels returns labels in a stable order for display.
func Labels(labels map[string]string) []string {
	out := make([]string, 0, len(labels))
	for k, v := range labels {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
