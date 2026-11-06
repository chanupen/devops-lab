package main

import (
	"context"
	"encoding/json"
	"testing"
)

type alertTools struct{ data string }

func (a alertTools) Call(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(a.data), nil
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
