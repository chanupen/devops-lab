package assist

import (
	"context"
	"encoding/json"
	"testing"
)

func TestEvaluateRequiresThreeRuns(t *testing.T) {
	fixture := EvalFixture{ID: "one", Alert: Alert{Name: "Example", Labels: map[string]string{}}, Expected: "not established",
		Tools: map[string]json.RawMessage{"prometheus_query": json.RawMessage(`{}`)}}
	model := &fakeModel{decisions: []Decision{{Note: &Note{
		WhatFired: []Claim{{Text: "fired", Evidence: []string{"E1"}}},
		Cause:     []Claim{{Text: "not established"}},
		Confirm:   []Claim{{Text: "inspect", Evidence: []string{"E1"}}},
		Fix:       []Claim{{Text: "investigate"}},
	}}}}
	if _, _, err := Evaluate(context.Background(), []EvalFixture{fixture}, model, "fixture", false, 2); err == nil {
		t.Fatal("expected minimum-runs error")
	}
}

func TestSummarizeEval(t *testing.T) {
	got := SummarizeEval([]EvalResult{
		{Model: "a", CauseCorrect: true, EvidenceSupport: 1},
		{Model: "a", CauseCorrect: false, EvidenceSupport: .5},
	})
	if len(got) != 1 || got[0].CauseCorrect != .5 || got[0].EvidenceSupport != .75 {
		t.Fatalf("summary = %+v", got)
	}
}
