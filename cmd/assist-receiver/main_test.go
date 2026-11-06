package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chanupen/devops-lab/internal/assist"
)

func TestWebhook(t *testing.T) {
	tests := []struct {
		name, method, body string
		status             int
		queued             int
	}{
		{"firing", "POST", `{"alerts":[{"status":"firing","labels":{"alertname":"A","namespace":"demo"}}]}`, 202, 1},
		{"resolved", "POST", `{"alerts":[{"status":"resolved","labels":{"alertname":"A"}}]}`, 204, 0},
		{"invalid", "POST", `{"alerts":[]}`, 400, 0},
		{"trailing data", "POST", `{"alerts":[{"status":"firing","labels":{"alertname":"A"}}]} {}`, 400, 0},
		{"oversize", "POST", `{"alerts":[{"labels":{"x":"` + strings.Repeat("x", 33000) + `"}}]}`, 400, 0},
		{"method", "GET", "", 405, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := make(chan []assist.Alert, 1)
			req := httptest.NewRequest(tt.method, "/webhook", strings.NewReader(tt.body))
			res := httptest.NewRecorder()
			webhook(queue).ServeHTTP(res, req)
			if res.Code != tt.status || len(queue) != tt.queued {
				t.Fatalf("status %d queue %d", res.Code, len(queue))
			}
		})
	}
}

func TestWebhookFullQueue(t *testing.T) {
	queue := make(chan []assist.Alert, 1)
	queue <- []assist.Alert{{Name: "existing"}}
	req := httptest.NewRequest(http.MethodPost, "/webhook",
		strings.NewReader(`{"alerts":[{"status":"firing","labels":{"alertname":"A"}}]}`))
	res := httptest.NewRecorder()
	webhook(queue).ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", res.Code)
	}
}
