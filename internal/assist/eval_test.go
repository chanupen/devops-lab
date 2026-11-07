package assist

import (
	"context"
	"encoding/json"
	"path/filepath"
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
		{Model: "a", CauseScored: true, CauseCorrect: true, EvidenceSupport: 1},
		{Model: "a", CauseScored: true, CauseCorrect: false, EvidenceSupport: .5},
	})
	if len(got) != 1 || got[0].CauseCorrect != .5 || got[0].EvidenceSupport != .75 {
		t.Fatalf("summary = %+v", got)
	}
}

func TestAbstentionAllowsCitedUncertainty(t *testing.T) {
	if !isAbstention([]Claim{{Text: "The root cause is not established.", Evidence: []string{"E1"}}}) {
		t.Fatal("cited uncertainty should count as an abstention")
	}
}

type fixtureModel struct{}

func (fixtureModel) Decide(_ context.Context, _ Alert, _ []Evidence) (Decision, error) {
	return Decision{Note: &Note{
		WhatFired: []Claim{{Text: "alert fired", Evidence: []string{"E1"}}},
		Cause:     []Claim{{Text: "The root cause is not established."}},
		Confirm:   []Claim{{Text: "inspect alert", Evidence: []string{"E1"}}},
		Fix:       []Claim{{Text: "investigate"}},
	}}, nil
}

func TestFixtureRuleDiagnosis(t *testing.T) {
	fixtures, err := LoadEvalFixtures(filepath.Join("testdata", "eval"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.ID, func(t *testing.T) {
			note, err := (Runner{
				Tools: FixtureTools(fixture.Tools), Model: fixtureModel{}, Tracer: evalTracer{}, RulesFirst: true,
			}).Run(context.Background(), fixture.Alert)
			if err != nil {
				t.Fatal(err)
			}
			var candidate string
			if len(note.Cause) > 0 {
				candidate = note.Cause[0].Text
			}
			clear := map[string]string{
				"bad-deploy": "bad-release", "oom": "memory-limit",
				"dns": "coredns-scaled-down", "node-loss": "worker-stopped",
			}
			if want, ok := clear[fixture.ID]; ok && (len(note.Cause) != 1 || note.Cause[0].CauseID != want) {
				t.Fatalf("candidate = %+v, want cause ID %q", note.Cause, want)
			}
			if (fixture.ID == "secret-drift" || fixture.ID == "slow-db") && !isAbstention(note.Cause) {
				t.Fatalf("candidate = %+v, want abstention", note.Cause)
			}
			t.Logf("expected=%q candidate=%q findings=%q", fixture.Expected, candidate, evidenceBySource(note.Evidence, "findings"))
		})
	}
}

func evidenceBySource(evidence []Evidence, source string) string {
	for _, item := range evidence {
		if item.Source == source {
			return item.Data
		}
	}
	return ""
}
