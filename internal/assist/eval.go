package assist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// EvalFixture is a recorded, safe-to-share evidence bundle with the drill's
// expected cause. Fixtures make model comparisons repeatable without faults.
type EvalFixture struct {
	ID         string                     `json:"id"`
	Alert      Alert                      `json:"alert"`
	Expected   string                     `json:"expected_cause"`
	Abstain    bool                       `json:"expect_abstention,omitempty"`
	RecordedAt time.Time                  `json:"recorded_at,omitempty"`
	Tools      map[string]json.RawMessage `json:"tools"`
}

// EvalResult is one model run against one fixture.
type EvalResult struct {
	Case            string        `json:"case"`
	Model           string        `json:"model"`
	Hybrid          bool          `json:"hybrid"`
	Findings        string        `json:"findings"`
	Evidence        []Evidence    `json:"evidence"`
	RuleFired       bool          `json:"rule_fired"`
	Candidate       *Claim        `json:"candidate,omitempty"`
	ModelCause      []Claim       `json:"model_cause"`
	FinalCause      []Claim       `json:"final_cause"`
	ExpectedCause   string        `json:"expected_cause"`
	Comparison      string        `json:"comparison"`
	CauseCorrect    bool          `json:"cause_correct"`
	Abstained       bool          `json:"abstained"`
	CauseScored     bool          `json:"cause_scored"`
	AbstainExpected bool          `json:"abstain_expected"`
	AbstainCorrect  bool          `json:"abstain_correct"`
	EvidenceSupport float64       `json:"evidence_support"`
	Duration        time.Duration `json:"duration"`
	Error           string        `json:"error,omitempty"`
}

// EvalSummary aggregates repeated results. Variance is population standard
// deviation because each requested run belongs to the measured set.
type EvalSummary struct {
	Model            string        `json:"model"`
	Hybrid           bool          `json:"hybrid"`
	Runs             int           `json:"runs"`
	CauseScored      int           `json:"cause_scored"`
	Abstentions      int           `json:"abstentions"`
	AbstainExpected  int           `json:"abstain_expected"`
	AbstainCorrect   int           `json:"abstain_correct"`
	CauseCorrect     float64       `json:"cause_correct"`
	EvidenceSupport  float64       `json:"evidence_support"`
	MeanDuration     time.Duration `json:"mean_duration"`
	DurationVariance time.Duration `json:"duration_stddev"`
}

// LoadEvalFixtures reads all JSON fixtures in directory in stable order.
func LoadEvalFixtures(dir string) ([]EvalFixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var fixtures []EvalFixture
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var fixture EvalFixture
		if err := json.Unmarshal(b, &fixture); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		if fixture.ID == "" || fixture.Alert.Name == "" || fixture.Expected == "" || len(fixture.Tools) == 0 {
			return nil, fmt.Errorf("%s: id, alert, expected_cause and tools are required", entry.Name())
		}
		fixtures = append(fixtures, fixture)
	}
	slices.SortFunc(fixtures, func(a, b EvalFixture) int { return strings.Compare(a.ID, b.ID) })
	if len(fixtures) == 0 {
		return nil, errors.New("no evaluation fixtures found")
	}
	return fixtures, nil
}

// FixtureTools returns recorded tool output and rejects calls not recorded by
// the fixture. The runner still applies all output bounds and redaction.
type FixtureTools map[string]json.RawMessage

// Call implements Tools with one recorded response per tool name.
func (t FixtureTools) Call(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
	b, ok := t[name]
	if !ok {
		return nil, fmt.Errorf("fixture has no %s response", name)
	}
	return b, nil
}

type evalTracer struct{}

func (evalTracer) Trace(context.Context, time.Time, time.Time, []ToolObservation, int, bool) (string, error) {
	return "eval", nil
}

type evalModel struct {
	Model
	evidence []Evidence
	cause    []Claim
}

func (m *evalModel) Decide(ctx context.Context, alert Alert, evidence []Evidence) (Decision, error) {
	m.evidence = append([]Evidence(nil), evidence...)
	decision, err := m.Model.Decide(ctx, alert, evidence)
	if decision.Note != nil {
		m.cause = append([]Claim(nil), decision.Note.Cause...)
	}
	return decision, err
}

// Evaluate runs each fixture at least three times and reports the metrics
// used for comparisons: cause accuracy, supported claims, and elapsed time.
func Evaluate(ctx context.Context, fixtures []EvalFixture, model Model, modelName string, hybrid bool, runs int, report func(EvalResult) error) ([]EvalResult, []EvalSummary, error) {
	if runs < 3 {
		return nil, nil, errors.New("evaluation requires at least 3 runs per case")
	}
	var results []EvalResult
	for _, fixture := range fixtures {
		for range runs {
			started := time.Now()
			recorded := &evalModel{Model: model}
			runner := Runner{Tools: FixtureTools(fixture.Tools), Model: recorded, Tracer: evalTracer{}, RulesFirst: hybrid}
			if !fixture.RecordedAt.IsZero() {
				runner.Now = func() time.Time { return fixture.RecordedAt }
			}
			note, err := runner.Run(ctx, fixture.Alert)
			expectAbstention := fixture.Abstain || fixture.Expected == "not established"
			result := EvalResult{
				Case: fixture.ID, Model: modelName, Hybrid: hybrid, Duration: time.Since(started),
				ModelCause: recorded.cause, FinalCause: note.Cause, ExpectedCause: fixture.Expected,
				Evidence:    recorded.evidence,
				CauseScored: !expectAbstention, AbstainExpected: expectAbstention,
			}
			for _, item := range recorded.evidence {
				if item.Source == "findings" {
					result.Findings = item.Data
				}
			}
			if hybrid {
				result.Candidate = ruleCandidate(recorded.evidence)
				result.RuleFired = result.Candidate != nil
			}
			if err != nil {
				result.Error = err.Error()
				result.Comparison = "run failed"
			} else {
				result.Abstained = isAbstention(note.Cause)
				result.AbstainCorrect = expectAbstention && result.Abstained
				if result.CauseScored {
					result.CauseCorrect, result.Comparison = compareCause(note.Cause, fixture.Expected)
				} else {
					result.Comparison = "excluded from cause accuracy; expected abstention scored separately"
				}
				result.EvidenceSupport = supportedClaims(note)
			}
			results = append(results, result)
			if report != nil {
				if err := report(result); err != nil {
					return results, nil, err
				}
			}
		}
	}
	return results, SummarizeEval(results), nil
}

func causeMatches(claims []Claim, expected string) bool {
	matched, _ := compareCause(claims, expected)
	return matched
}

func compareCause(claims []Claim, expected string) (bool, string) {
	if expected == "not established" {
		return isAbstention(claims), "explicit uncertainty rather than an asserted cause"
	}
	phrases := map[string][]string{
		"memory-limit":        {"memory limit is too low", "memory limit was too low", "insufficient memory limit", "exceeded its memory limit", "exceeded the memory limit"},
		"bad-release":         {"bad release", "faulty release", "bad deployment", "faulty image"},
		"issuer-ca-secret":    {"missing ca secret", "ca secret is missing", "ca secret not found"},
		"coredns-scaled-down": {"coredns has been scaled to zero", "coredns is scaled to zero", "coredns replicas 0"},
		"worker-stopped":      {"worker was stopped", "worker is stopped", "worker node is stopped", "stopped worker"},
	}
	for _, claim := range claims {
		if claim.CauseID != "" {
			if claim.CauseID == expected {
				return true, "exact cause ID"
			}
			continue
		}
		text := strings.ToLower(claim.Text)
		matches := func(phrase string) bool {
			return strings.Contains(text, phrase) &&
				!strings.Contains(text, "not "+phrase) &&
				!strings.Contains(text, "not a "+phrase) &&
				!strings.Contains(text, "no "+phrase)
		}
		if matches(strings.ToLower(expected)) {
			return true, "literal expected label in final cause text"
		}
		for _, phrase := range phrases[expected] {
			if matches(phrase) {
				return true, "accepted causal phrase: " + phrase
			}
		}
	}
	return false, "no matching cause ID, label, or accepted causal phrase"
}

func isAbstention(claims []Claim) bool {
	if len(claims) != 1 || claims[0].CauseID != "" {
		return false
	}
	text := strings.ToLower(claims[0].Text)
	if strings.Contains(text, "the container was oom") {
		return false
	}
	for _, expected := range []string{"memory-limit", "bad-release", "issuer-ca-secret", "coredns-scaled-down", "worker-stopped"} {
		if matched, _ := compareCause(claims, expected); matched {
			return false
		}
	}
	for _, phrase := range []string{
		"not established", "not determined", "insufficient evidence",
		"cannot establish the cause", "not sufficient to establish",
		"further investigation is required",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func supportedClaims(note Note) float64 {
	claims := append(append(append([]Claim{}, note.WhatFired...), note.Cause...), note.Confirm...)
	claims = append(claims, note.Fix...)
	if len(claims) == 0 {
		return 0
	}
	supported := 0
	for _, claim := range claims {
		if !claim.Unsupported && claimSupported(claim, note.Evidence) {
			supported++
		}
	}
	return float64(supported) / float64(len(claims))
}

func claimSupported(claim Claim, evidence []Evidence) bool {
	byID := make(map[string]string, len(evidence))
	for _, item := range evidence {
		byID[item.ID] = strings.ToLower(item.Data)
	}
	for _, id := range claim.Evidence {
		data, ok := byID[id]
		if !ok {
			return false
		}
		for _, word := range strings.FieldsFunc(strings.ToLower(claim.Text), func(r rune) bool {
			return r < 'a' || r > 'z'
		}) {
			if len(word) >= 4 && strings.Contains(data, word) {
				return true
			}
		}
	}
	return false
}

// SummarizeEval builds one summary per model/mode pair.
func SummarizeEval(results []EvalResult) []EvalSummary {
	type total struct {
		EvalSummary
		durations []float64
	}
	totals := map[string]*total{}
	for _, result := range results {
		key := result.Model + fmt.Sprintf("/%t", result.Hybrid)
		item := totals[key]
		if item == nil {
			item = &total{EvalSummary: EvalSummary{Model: result.Model, Hybrid: result.Hybrid}}
			totals[key] = item
		}
		item.Runs++
		if result.CauseScored {
			item.CauseScored++
		}
		if result.CauseCorrect {
			item.CauseCorrect++
		}
		if result.Abstained {
			item.Abstentions++
		}
		if result.AbstainExpected {
			item.AbstainExpected++
		}
		if result.AbstainCorrect {
			item.AbstainCorrect++
		}
		item.EvidenceSupport += result.EvidenceSupport
		item.durations = append(item.durations, float64(result.Duration))
	}
	out := make([]EvalSummary, 0, len(totals))
	for _, item := range totals {
		n := float64(item.Runs)
		if item.CauseScored > 0 {
			item.CauseCorrect /= float64(item.CauseScored)
		}
		item.EvidenceSupport /= n
		var mean float64
		for _, duration := range item.durations {
			mean += duration
		}
		mean /= n
		var variance float64
		for _, duration := range item.durations {
			delta := duration - mean
			variance += delta * delta
		}
		item.MeanDuration = time.Duration(mean)
		item.DurationVariance = time.Duration(math.Sqrt(variance / n))
		out = append(out, item.EvalSummary)
	}
	slices.SortFunc(out, func(a, b EvalSummary) int {
		if a.Model == b.Model {
			if a.Hybrid == b.Hybrid {
				return 0
			}
			if !a.Hybrid {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Model, b.Model)
	})
	return out
}
