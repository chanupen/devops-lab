package assist

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OTLPTracer sends only bounded run metadata to the local Langfuse instance.
type OTLPTracer struct {
	URL       string
	PublicKey string
	SecretKey string
	Client    *http.Client
}

// Trace records one Langfuse observation; keys and evidence are never attributes.
func (t OTLPTracer) Trace(ctx context.Context, start, end time.Time, evidence []ToolObservation, calls int, incomplete bool) (string, error) {
	u, err := url.Parse(t.URL)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		((u.Scheme != "http" || u.Host != "langfuse-web.ai.svc:3000") &&
			(u.Scheme != "https" || u.Host != "langfuse.localtest.me:8443")) {
		return "", errors.New("invalid local Langfuse endpoint")
	}
	if t.PublicKey == "" || t.SecretKey == "" {
		return "", errors.New("langfuse keys are unavailable")
	}
	var ids [24]byte
	if _, err := rand.Read(ids[:]); err != nil {
		return "", errors.New("cannot generate trace ID")
	}
	id := hex.EncodeToString(ids[:16])
	attr := func(key, value string) map[string]any {
		return map[string]any{"key": key, "value": map[string]string{"stringValue": value}}
	}
	observations, err := json.Marshal(evidence)
	if err != nil {
		return "", errors.New("cannot encode trace metadata")
	}
	span := map[string]any{
		"traceId": id, "spanId": hex.EncodeToString(ids[16:]), "name": "lab-assist",
		"startTimeUnixNano": fmt.Sprint(start.UnixNano()), "endTimeUnixNano": fmt.Sprint(end.UnixNano()),
		"attributes": []any{
			attr("langfuse.observation.type", "span"),
			attr("lab.evidence.count", fmt.Sprint(len(evidence))),
			attr("lab.evidence.observations", string(observations)),
			attr("lab.extra_calls", fmt.Sprint(calls)),
			attr("lab.incomplete", fmt.Sprint(incomplete)),
		},
	}
	body := map[string]any{"resourceSpans": []any{map[string]any{
		"resource":   map[string]any{"attributes": []any{attr("service.name", "lab-assist")}},
		"scopeSpans": []any{map[string]any{"scope": map[string]string{"name": "lab-assist"}, "spans": []any{span}}},
	}}}
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/public/otel/v1/traces"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(t.PublicKey, t.SecretKey)
	req.Header.Set("Content-Type", "application/json")
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{}, CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("langfuse redirects are not allowed")
		}}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("langfuse request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("langfuse returned HTTP %d", resp.StatusCode)
	}
	response, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(response) > 4096 {
		return "", errors.New("langfuse response could not be read")
	}
	var result struct {
		PartialSuccess struct {
			RejectedSpans json.Number `json:"rejectedSpans"`
		} `json:"partialSuccess"`
	}
	if len(response) > 0 {
		if err := json.Unmarshal(response, &result); err != nil {
			return "", errors.New("invalid langfuse response")
		}
		if result.PartialSuccess.RejectedSpans != "" && result.PartialSuccess.RejectedSpans != "0" {
			return "", errors.New("langfuse rejected the trace")
		}
	}
	return id, nil
}
