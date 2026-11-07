package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chanupen/devops-lab/internal/assist"
)

type alertTools struct{ data string }

func (a alertTools) Call(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(a.data), nil
}

func TestEvalCasesSeparateAbstentionsAndFailures(t *testing.T) {
	var out bytes.Buffer
	err := writeAssistEvalCases(&out, []assist.EvalResult{
		{Case: "slow-db", Model: "fixture", AbstainExpected: true, AbstainCorrect: true},
		{Case: "oom", Model: "fixture", CauseScored: true, Error: "model failed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "not scored") || !strings.Contains(text, "1/1") || !strings.Contains(text, "0.0%") {
		t.Fatalf("cases = %s", text)
	}
}

func TestFindAlert(t *testing.T) {
	tools := alertTools{`{"status":"success","data":{"result":[{"metric":{"namespace":"demo","pod":"a"}},{"metric":{"namespace":"other","pod":"b"}}]}}`}
	for _, tt := range []struct {
		name, namespace string
		labels          map[string]string
		wantErr         bool
	}{
		{"ambiguous", "", nil, true},
		{"namespace", "demo", nil, false},
		{"labels", "", map[string]string{"pod": "a"}, false},
		{"missing", "missing", nil, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			alert, err := findAlert(context.Background(), tools, "Example", tt.namespace, tt.labels)
			if (err != nil) != tt.wantErr || (err == nil && alert.Labels["pod"] != "a") {
				t.Fatalf("alert %+v, error %v", alert, err)
			}
		})
	}
}
