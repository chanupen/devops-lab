package assist

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type modelTransport struct {
	body string
}

func (t *modelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(req.Body)
	t.body = string(b)
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{"message":{"content":"{\"note\":{\"what_fired\":[],\"likely_cause\":[],\"commands_to_confirm\":[],\"fix\":[]}}"}}`))}, nil
}

func TestOllamaSettings(t *testing.T) {
	rt := &modelTransport{}
	model := HTTPModel{URL: "http://127.0.0.1:11434", Name: "llama3.2:3b", Client: &http.Client{Transport: rt}}
	if _, err := model.Decide(context.Background(), Alert{Name: "A"}, []Evidence{{ID: "E1"}, {ID: "E2"}}); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Format  json.RawMessage `json:"format"`
		Options struct {
			Temperature int `json:"temperature"`
			Seed        int `json:"seed"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(rt.body), &body); err != nil || body.Options.Temperature != 0 || body.Options.Seed != 42 || !json.Valid(body.Format) || len(body.Format) == 0 || body.Format[0] != '{' {
		t.Fatalf("settings %s, error %v", rt.body, err)
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(body.Format, &schema); err != nil {
		t.Fatal(err)
	}
	// A root oneOf causes Ollama to ignore the nested constraints.
	if _, ok := schema["oneOf"]; ok {
		t.Fatal("Ollama schema must not use root oneOf")
	}
	var defs struct {
		Claims struct {
			MaxItems int `json:"maxItems"`
			Items    struct {
				Properties struct {
					Evidence struct {
						MaxItems int `json:"maxItems"`
						Items    struct {
							Enum []string `json:"enum"`
						} `json:"items"`
					} `json:"evidence"`
				} `json:"properties"`
			} `json:"items"`
		} `json:"claims"`
	}
	if err := json.Unmarshal(schema["$defs"], &defs); err != nil || defs.Claims.MaxItems < 1 || defs.Claims.Items.Properties.Evidence.MaxItems < 1 {
		t.Fatal("Ollama schema must bound claims and citations")
	}
	if got := strings.Join(defs.Claims.Items.Properties.Evidence.Items.Enum, ","); got != "E1,E2" {
		t.Fatalf("citation IDs = %q", got)
	}
	format := string(body.Format)
	if !(strings.Index(format, `"what_fired"`) < strings.Index(format, `"likely_cause"`) &&
		strings.Index(format, `"likely_cause"`) < strings.Index(format, `"commands_to_confirm"`) &&
		strings.Index(format, `"commands_to_confirm"`) < strings.Index(format, `"fix"`)) {
		t.Fatal("Ollama schema must keep incident sections in reasoning order")
	}
}

func TestHostedRequiresExplicitConfiguration(t *testing.T) {
	rt := &modelTransport{}
	for _, m := range []HTTPModel{
		{URL: "https://example.com", Name: "model"},
		{URL: "http://example.com", Name: "model", Hosted: true, Key: "key"},
		{URL: "https://example.com", Name: "model", Hosted: true},
	} {
		m.Client = &http.Client{Transport: rt}
		if _, err := m.Decide(context.Background(), Alert{Name: "A"}, nil); err == nil {
			t.Fatal("model configuration unexpectedly accepted")
		}
		if rt.body != "" {
			t.Fatal("request sent before opt-in validation")
		}
	}
}

type traceTransport struct {
	body  string
	auth  string
	reply string
}

func (t *traceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(req.Body)
	t.body = string(b)
	t.auth = req.Header.Get("Authorization")
	reply := t.reply
	if reply == "" {
		reply = "{}"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(reply))}, nil
}

func TestTraceRejectsPartialSuccess(t *testing.T) {
	rt := &traceTransport{reply: `{"partialSuccess":{"rejectedSpans":"1"}}`}
	tracer := OTLPTracer{URL: "http://langfuse-web.ai.svc:3000", PublicKey: "public", SecretKey: "secret",
		Client: &http.Client{Transport: rt}}
	if _, err := tracer.Trace(context.Background(), time.Now(), time.Now(), nil, 0, false); err == nil {
		t.Fatal("rejected span was reported as traced")
	}
}

func TestTraceExcludesKeysAndEvidence(t *testing.T) {
	rt := &traceTransport{}
	tracer := OTLPTracer{URL: "http://langfuse-web.ai.svc:3000", PublicKey: "public-fixture", SecretKey: "secret-fixture",
		Client: &http.Client{Transport: rt}}
	id, err := tracer.Trace(context.Background(), time.Now(), time.Now(), []ToolObservation{{EvidenceID: "E1", Source: "pods"}}, 1, false)
	if err != nil || len(id) != 32 || !strings.HasPrefix(rt.auth, "Basic ") {
		t.Fatalf("trace %q, error %v", id, err)
	}
	for _, s := range []string{"public-fixture", "secret-fixture", "evidence body"} {
		if strings.Contains(rt.body, s) {
			t.Fatalf("trace contains %q: %s", s, rt.body)
		}
	}
}
