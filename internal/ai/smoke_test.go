package ai

import (
	"strings"
	"testing"
)

func TestSmokeJob(t *testing.T) {
	j := SmokeJob("lab-ai-smoke-1", "llama3.2:3b")
	pod := j.Spec.Template
	if pod.Labels[OllamaLabel] != "true" {
		t.Errorf("pod labels %v: the NetworkPolicy lets only %s=true reach Ollama", pod.Labels, OllamaLabel)
	}
	c := pod.Spec.Containers[0]
	if !strings.Contains(c.Image, "@sha256:") || c.Resources.Requests.Memory().IsZero() || *j.Spec.BackoffLimit != 0 {
		t.Errorf("container %+v", c)
	}
	if c.Command[0] != "python3" || !strings.Contains(c.Command[3], "/api/public/otel/v1/traces") {
		t.Errorf("command %v", c.Command[:2])
	}
	var keys []string
	for _, e := range c.Env {
		if e.ValueFrom != nil {
			keys = append(keys, e.ValueFrom.SecretKeyRef.Name+"/"+e.ValueFrom.SecretKeyRef.Key)
		}
	}
	if strings.Join(keys, ",") != "langfuse-credentials/public-key,langfuse-credentials/secret-key" {
		t.Errorf("secret keys %v", keys)
	}
}

func TestParseSmoke(t *testing.T) {
	ok := `{"model":"llama3.2:3b","answer":"It restarts the container.","latencySeconds":1.2,"outputTokens":8,"outputTokensPerSecond":40.1,"traceId":"ab","traceStatus":200}`
	tests := []struct {
		name, out, wantErr string
	}{
		{"ok", "pulling\n" + ok + "\n", ""},
		{"no answer", `{"answer":"","outputTokens":0,"traceStatus":200}`, "no answer"},
		{"trace refused", `{"answer":"x","outputTokens":1,"traceStatus":401}`, "HTTP 401"},
		{"not JSON", "Traceback (most recent call last):\nURLError", "smoke output"},
	}
	for _, tt := range tests {
		r, err := ParseSmoke(tt.out)
		if tt.wantErr == "" {
			if err != nil || r.OutputTokensPerSecond != 40.1 || r.TraceID != "ab" {
				t.Errorf("%s: %+v %v", tt.name, r, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.wantErr)
		}
	}
}
