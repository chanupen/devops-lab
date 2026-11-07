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
	ID       string                     `json:"id"`
	Alert    Alert                      `json:"alert"`
	Expected string                     `json:"expected_cause"`
	Tools    map[string]json.RawMessage `json:"tools"`
}

// EvalResult is one model run against one fixture.
type EvalResult struct {
	Case            string        `json:"case"`
	Model           string        `json:"model"`
	Hybrid          bool          `json:"hybrid"`
	CauseCorrect    bool          `json:"cause_correct"`
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

// Evaluate runs each fixture at least three times and reports the metrics
// used for comparisons: cause accuracy, supported claims, and elapsed time.
func Evaluate(ctx context.Context, fixtures []EvalFixture, model Model, modelName string, hybrid bool, runs int) ([]EvalResult, []EvalSummary, error) {
	if runs < 3 {
		return nil, nil, errors.New("evaluation requires at least 3 runs per case")
	}
	var results []EvalResult
	for _, fixture := range fixtures {
		for range runs {
			started := time.Now()
			note, err := (Runner{Tools: FixtureTools(fixture.Tools), Model: model, Tracer: evalTracer{}, RulesFirst: hybrid}).Run(ctx, fixture.Alert)
			result := EvalResult{Case: fixture.ID, Model: modelName, Hybrid: hybrid, Duration: time.Since(started)}
			if err != nil {
				result.Error = err.Error()
			} else {
				result.CauseCorrect = causeMatches(note.Cause, fixture.Expected)
				result.EvidenceSupport = supportedClaims(note)
			}
			results = append(results, result)
		}
	}
	return results, SummarizeEval(results), nil
}

func causeMatches(claims []Claim, expected string) bool {
	for _, claim := range claims {
		if strings.Contains(strings.ToLower(claim.Text), strings.ToLower(expected)) {
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
		if result.CauseCorrect {
			item.CauseCorrect++
		}
		item.EvidenceSupport += result.EvidenceSupport
		item.durations = append(item.durations, float64(result.Duration))
	}
	out := make([]EvalSummary, 0, len(totals))
	for _, item := range totals {
		n := float64(item.Runs)
		item.CauseCorrect /= n
		item.EvidenceSupport /= n
		var mean float64
		for _, duration := range item.durations {
			mean += duration
		}
		mean /= n
		var variance float64
		for _, duration := range item.durations {
			variance += math.Pow(duration-mean, 2)
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
