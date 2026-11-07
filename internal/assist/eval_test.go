package assist

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if _, _, err := Evaluate(context.Background(), []EvalFixture{fixture}, model, "fixture", false, 2, nil); err == nil {
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

func TestEvaluateRetainsModelCauseBeforeRuleOverride(t *testing.T) {
	fixtures, err := LoadEvalFixtures(filepath.Join("testdata", "eval"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		if fixture.ID != "oom" {
			continue
		}
		results, _, err := Evaluate(context.Background(), []EvalFixture{fixture}, fixtureModel{}, "fixture", true, 3, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range results {
			if !isAbstention(result.ModelCause) || !result.RuleFired ||
				result.Candidate.CauseID != "memory-limit" || !result.CauseCorrect {
				t.Fatalf("diagnostic result = %+v", result)
			}
		}
	}
}

func TestEvaluateErrorsRemainInDenominator(t *testing.T) {
	fixture := EvalFixture{ID: "one", Alert: Alert{Name: "Example"}, Expected: "memory-limit"}
	results, summary, err := Evaluate(context.Background(), []EvalFixture{fixture}, &fakeModel{}, "fixture", false, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary[0].CauseScored != 3 || summary[0].CauseCorrect != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	for _, result := range results {
		if result.Error == "" || !result.CauseScored {
			t.Fatalf("failed result = %+v", result)
		}
	}
}

func TestAbstentionAllowsCitedUncertainty(t *testing.T) {
	if !isAbstention([]Claim{{Text: "The root cause is not established.", Evidence: []string{"E1"}}}) {
		t.Fatal("cited uncertainty should count as an abstention")
	}
}

func TestCauseGrading(t *testing.T) {
	for _, tt := range []struct {
		name, text, id, expected string
		want                     bool
	}{
		{"causal phrase", "The issuer references a missing CA Secret.", "", "issuer-ca-secret", true},
		{"limit diagnosis", "The memory limit was too low.", "", "memory-limit", true},
		{"symptom only", "The container was OOM killed.", "", "memory-limit", false},
		{"confirmation only", "Inspect memory limits and node pressure.", "", "memory-limit", false},
		{"negated release", "It is not a bad release.", "", "bad-release", false},
		{"conflicting ID", "The bad-release was responsible.", "worker-stopped", "bad-release", false},
		{"uncertainty wording", "Further investigation is required.", "", "not established", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := causeMatches([]Claim{{Text: tt.text, CauseID: tt.id}}, tt.expected); got != tt.want {
				t.Fatalf("cause match = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAbstentionDoesNotHideAnAssertedOOM(t *testing.T) {
	for _, text := range []string{
		"The container was OOM killed. Further investigation is required.",
		"The memory limit was too low. Further investigation is required.",
	} {
		if isAbstention([]Claim{{Text: text}}) {
			t.Fatalf("asserted diagnosis must not be counted as abstention: %s", text)
		}
	}
}

func TestKnownMistakesAreScoredAsAbstentions(t *testing.T) {
	fixture := EvalFixture{ID: "known", Alert: Alert{Name: "Example"}, Expected: "not established"}
	_, summaries, err := Evaluate(context.Background(), []EvalFixture{fixture}, fixtureModel{}, "fixture", false, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summaries[0].CauseScored != 0 || summaries[0].AbstainExpected != 3 || summaries[0].AbstainCorrect != 3 {
		t.Fatalf("summary = %+v", summaries)
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
			runner := Runner{
				Tools: FixtureTools(fixture.Tools), Model: fixtureModel{}, Tracer: evalTracer{}, RulesFirst: true,
			}
			if !fixture.RecordedAt.IsZero() {
				runner.Now = func() time.Time { return fixture.RecordedAt }
			}
			note, err := runner.Run(context.Background(), fixture.Alert)
			if err != nil {
				t.Fatal(err)
			}
			var candidate string
			if len(note.Cause) > 0 {
				candidate = note.Cause[0].Text
			}
			clearCase := map[string]string{
				"bad-deploy": "bad-release", "oom": "memory-limit",
				"dns": "coredns-scaled-down", "node-loss": "worker-stopped",
				"cert-expiry":     "issuer-ca-secret",
				"live-bad-deploy": "bad-release",
			}
			if want, ok := clearCase[fixture.ID]; ok && (len(note.Cause) != 1 || note.Cause[0].CauseID != want) {
				t.Fatalf("candidate = %+v, want cause ID %q", note.Cause, want)
			}
			if (fixture.ID == "secret-drift" || fixture.ID == "slow-db") && !isAbstention(note.Cause) {
				t.Fatalf("candidate = %+v, want abstention", note.Cause)
			}
			t.Logf("expected=%q candidate=%q findings=%q", fixture.Expected, candidate, evidenceBySource(note.Evidence, "findings"))
		})
	}
}

func TestLiveReleaseFixtureReplay(t *testing.T) {
	fixtures, err := LoadEvalFixtures(filepath.Join("testdata", "eval"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		if fixture.ID != "live-bad-deploy" {
			continue
		}
		results, _, err := Evaluate(context.Background(), []EvalFixture{fixture}, fixtureModel{}, "fixture", true, 3, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range results {
			if !result.CauseCorrect || result.Candidate == nil || result.Candidate.CauseID != "bad-release" || len(result.Candidate.Evidence) != 2 {
				t.Fatalf("live fixture diagnosis = %+v", result)
			}
			pod := evidenceBySource(result.Evidence, "pods")
			if len(pod) > evidenceLimit || !json.Valid([]byte(pod)) || !strings.Contains(pod, "lab-zot:5000/lab/podinfo:6.15.1") {
				t.Fatalf("live pod evidence is unusable: %s", pod)
			}
		}
		return
	}
	t.Fatal("recorded live bad-deploy fixture missing")
}

func evidenceBySource(evidence []Evidence, source string) string {
	for _, item := range evidence {
		if item.Source == source {
			return item.Data
		}
	}
	return ""
}
