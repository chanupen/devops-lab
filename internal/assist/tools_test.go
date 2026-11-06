package assist

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type recorder struct {
	requests []*http.Request
	status   int
	body     string
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.requests = append(r.requests, req.Clone(req.Context()))
	body := r.body
	if body == "" {
		switch {
		case strings.HasSuffix(req.URL.Path, "/pods/example"):
			body = `{"metadata":{"name":"example","namespace":"demo"},"spec":{"nodeName":"node1","containers":[{"name":"app","resources":{"limits":{"memory":"64Mi"}},"env":[{"name":"PASSWORD","value":"do-not-return"}]}]},"status":{"phase":"Running"}}`
		case strings.HasSuffix(req.URL.Path, "/pods"):
			body = `{"metadata":{"continue":"next-page"},"items":[{"metadata":{"name":"example","namespace":"demo"},"status":{"phase":"Running"}}]}`
		case strings.HasSuffix(req.URL.Path, "/events"):
			body = `{"items":[{"reason":"Unhealthy","message":"probe failed"}]}`
		case strings.Contains(req.URL.Path, "/applications/"):
			body = `{"spec":{"secret":"do-not-return"},"status":{"history":[{"id":1,"revision":"abc","deployedAt":"2026-01-01T00:00:00Z","source":{"password":"do-not-return"}}]}}`
		default:
			body = `{"status":"success","data":{}}`
		}
	}
	status := r.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func testClient(r *recorder) *Client {
	return newClient(&url.URL{Scheme: "https", Host: "kube.example:6443"}, r)
}

func TestAllowedTools(t *testing.T) {
	tests := []struct {
		name, args string
		paths      []string
	}{
		{"events", `{"namespace":"demo","pod":"example"}`, []string{"/api/v1/namespaces/demo/events"}},
		{"pods", `{"namespace":"demo"}`, []string{"/api/v1/namespaces/demo/pods"}},
		{"pods", `{"namespace":"demo","pod":"example"}`, []string{"/api/v1/namespaces/demo/pods/example"}},
		{"pod_describe", `{"namespace":"demo","pod":"example"}`, []string{"/api/v1/namespaces/demo/pods/example", "/api/v1/namespaces/demo/events"}},
		{"argocd_history", `{"app":"frontend"}`, []string{"/apis/argoproj.io/v1alpha1/namespaces/argocd/applications/frontend"}},
		{"loki_logs", `{"query":"{app=\"frontend\"}","start":"2026-01-01T00:00:00Z","end":"2026-01-01T00:30:00Z"}`, []string{"/api/v1/namespaces/monitoring/services/loki:3100/proxy/loki/api/v1/query_range"}},
		{"prometheus_query", `{"query":"up","end":"2026-01-01T00:00:00Z"}`, []string{"/api/v1/namespaces/monitoring/services/kps-prometheus:9090/proxy/api/v1/query"}},
	}
	for _, tt := range tests {
		t.Run(tt.name+tt.args, func(t *testing.T) {
			r := &recorder{}
			out, err := testClient(r).Call(context.Background(), tt.name, json.RawMessage(tt.args))
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(out) || strings.Contains(string(out), "do-not-return") {
				t.Fatalf("unsafe or invalid output: %s", out)
			}
			if (tt.name == "pod_describe" || (tt.name == "pods" && strings.Contains(tt.args, `"pod"`))) &&
				!strings.Contains(string(out), `"memory":"64Mi"`) {
				t.Fatalf("pod resource limit missing: %s", out)
			}
			if tt.name == "pods" && !strings.Contains(tt.args, `"pod"`) &&
				!strings.Contains(string(out), `"truncated":true`) {
				t.Fatalf("list truncation missing: %s", out)
			}
			if len(r.requests) != len(tt.paths) {
				t.Fatalf("got %d requests, want %d", len(r.requests), len(tt.paths))
			}
			for i, req := range r.requests {
				if req.Method != http.MethodGet || req.URL.Path != tt.paths[i] {
					t.Errorf("request %d = %s %s", i, req.Method, req.URL)
				}
			}
		})
	}
}

func TestClientUsesBearerTokenOverTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespaces/demo/pods/example" ||
			r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"metadata":{"name":"example","namespace":"demo"},"status":{"phase":"Running"}}`)
	}))
	defer server.Close()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	c, err := NewClient(&rest.Config{
		Host: server.URL, BearerToken: "test-token", TLSClientConfig: rest.TLSClientConfig{CAData: cert},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Call(context.Background(), "pods", json.RawMessage(`{"namespace":"demo","pod":"example"}`))
	if err != nil || !strings.Contains(string(out), `"phase":"Running"`) {
		t.Fatalf("result = %s, error = %v", out, err)
	}
}

func TestClientRefusesRedirect(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, "/api/v1/namespaces/demo/secrets", http.StatusFound)
	}))
	defer server.Close()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	c, err := NewClient(&rest.Config{
		Host: server.URL, BearerToken: "test-token", TLSClientConfig: rest.TLSClientConfig{CAData: cert},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Call(context.Background(), "pods", json.RawMessage(`{"namespace":"demo","pod":"example"}`))
	if err == nil || requests != 1 {
		t.Fatalf("redirect: error %v, requests %d", err, requests)
	}
}

func TestRejectedCallsNeverReachTransport(t *testing.T) {
	tests := []struct{ name, args string }{
		{"shell", `{}`},
		{"exec", `{"namespace":"demo","pod":"example"}`},
		{"secrets", `{"namespace":"demo"}`},
		{"pods", `{"namespace":"demo","verb":"delete"}`},
		{"pods", `{"namespace":"demo","url":"https://elsewhere"}`},
		{"pods", `{"namespace":"demo","pod":"../secrets"}`},
		{"events", `{"namespace":"demo","pod":"p,metadata.name=x"}`},
		{"argocd_history", `{"app":"../secrets"}`},
		{"loki_logs", `{"query":"{}","start":"2026-01-01T00:00:00Z","end":"2026-01-01T02:00:00Z"}`},
		{"prometheus_query", `{"query":"up","start":"2026-01-01T00:00:00Z"}`},
		{"prometheus_query", `{"query":"up"} {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name+tt.args, func(t *testing.T) {
			r := &recorder{}
			if _, err := testClient(r).Call(context.Background(), tt.name, json.RawMessage(tt.args)); err == nil {
				t.Fatal("expected rejection")
			}
			if len(r.requests) != 0 {
				t.Fatalf("rejected call reached transport: %v", r.requests)
			}
		})
	}
}

func TestTransportRejectsBeforeNetwork(t *testing.T) {
	good := "https://kube.example:6443/api/v1/namespaces/demo/pods/example"
	tests := []struct {
		method, target string
		body           bool
	}{
		{"POST", good, false},
		{"PUT", good, false},
		{"PATCH", good, false},
		{"DELETE", good, false},
		{"GET", good, true},
		{"GET", "https://other.example/api/v1/namespaces/demo/pods/example", false},
		{"GET", "https://kube.example:6443/api/v1/namespaces/demo/secrets", false},
		{"GET", "https://kube.example:6443/api/v1/namespaces/demo/pods/example/exec", false},
		{"GET", "https://kube.example:6443/api/v1/namespaces/demo/pods/example/log", false},
		{"GET", "https://kube.example:6443/api/v1/namespaces/demo/pods/example?watch=true", false},
		{"GET", "https://kube.example:6443/api/v1/namespaces/demo/pods/%65xample", false},
		{"GET", "https://kube.example:6443/api/v1/namespaces/monitoring/services/kps-prometheus:9090/proxy/api/v1/admin/tsdb/delete_series", false},
		{"GET", "https://kube.example:6443/api/v1/namespaces/monitoring/services/loki:3100/proxy/loki/api/v1/delete", false},
		{"GET", "https://kube.example:6443/api/v1/namespaces/monitoring/services/kps-prometheus:9090/proxy/api/v1/query?query=up&query=down", false},
	}
	for _, tt := range tests {
		t.Run(tt.method+tt.target, func(t *testing.T) {
			r := &recorder{}
			var body io.Reader
			if tt.body {
				body = strings.NewReader("x")
			}
			req, err := http.NewRequest(tt.method, tt.target, body)
			if err != nil {
				t.Fatal(err)
			}
			g := &guardTransport{base: &url.URL{Scheme: "https", Host: "kube.example:6443"}, next: r}
			if _, err := g.RoundTrip(req); err == nil {
				t.Fatal("expected rejection")
			}
			if len(r.requests) != 0 {
				t.Fatal("request reached underlying transport")
			}
		})
	}
}

func TestClientRejectsEndpointsBeforeTransport(t *testing.T) {
	tests := []string{
		"/api/v1/namespaces/demo/secrets",
		"/api/v1/namespaces/demo/pods/example/exec",
		"/api/v1/namespaces/monitoring/services/kps-prometheus:9090/proxy/api/v1/admin/tsdb/delete_series",
		"/api/v1/namespaces/monitoring/services/loki:3100/proxy/loki/api/v1/delete",
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			r := &recorder{}
			if _, err := testClient(r).get(context.Background(), path, nil); err == nil || !strings.Contains(err.Error(), "endpoint denied") {
				t.Fatalf("got %v, want client rejection", err)
			}
			if len(r.requests) != 0 {
				t.Fatal("request reached underlying transport")
			}
		})
	}
}

func TestTransportRejectsOverrides(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*http.Request)
	}{
		{"method override", func(r *http.Request) { r.Header.Set("X-HTTP-Method-Override", "DELETE") }},
		{"upgrade", func(r *http.Request) { r.Header.Set("Upgrade", "websocket") }},
		{"host override", func(r *http.Request) { r.Host = "other.example" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &recorder{}
			req, err := http.NewRequest(http.MethodGet, "https://kube.example:6443/api/v1/namespaces/demo/pods/example", nil)
			if err != nil {
				t.Fatal(err)
			}
			tt.modify(req)
			g := &guardTransport{base: &url.URL{Scheme: "https", Host: "kube.example:6443"}, next: r}
			if _, err := g.RoundTrip(req); err == nil || len(r.requests) != 0 {
				t.Fatalf("got error %v and %d requests", err, len(r.requests))
			}
		})
	}
}

func TestNewClientRequiresRestrictedAuthentication(t *testing.T) {
	valid := func() *rest.Config {
		return &rest.Config{Host: "https://127.0.0.1:6443", BearerToken: "test-token"}
	}
	tests := []struct {
		name string
		cfg  *rest.Config
		ok   bool
	}{
		{"bearer", valid(), true},
		{"nil", nil, false},
		{"no token", &rest.Config{Host: "https://127.0.0.1:6443"}, false},
		{"exec credential", &rest.Config{Host: "https://127.0.0.1:6443", BearerToken: "test", ExecProvider: &clientcmdapi.ExecConfig{}}, false},
		{"impersonation", &rest.Config{Host: "https://127.0.0.1:6443", BearerToken: "test", Impersonate: rest.ImpersonationConfig{UserName: "admin"}}, false},
		{"insecure", &rest.Config{Host: "https://127.0.0.1:6443", BearerToken: "test", TLSClientConfig: rest.TLSClientConfig{Insecure: true}}, false},
		{"plaintext", &rest.Config{Host: "http://127.0.0.1:6443", BearerToken: "test"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient(tt.cfg)
			if (err == nil) != tt.ok {
				t.Fatalf("NewClient error = %v", err)
			}
		})
	}
}

func TestToolFailures(t *testing.T) {
	tests := []struct {
		name string
		r    recorder
		want string
	}{
		{"status", recorder{status: http.StatusForbidden}, "HTTP 403"},
		{"invalid JSON", recorder{body: "oops"}, "not JSON"},
		{"oversize", recorder{body: `"` + strings.Repeat("x", maxResponse) + `"`}, "size limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := testClient(&tt.r).Call(context.Background(), "pods", json.RawMessage(`{"namespace":"demo"}`))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport := &cancelTransport{}
	c := newClient(&url.URL{Scheme: "https", Host: "kube.example:6443"}, transport)
	if _, err := c.Call(ctx, "pods", json.RawMessage(`{"namespace":"demo"}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled call = %v", err)
	}
}

type cancelTransport struct{}

func (*cancelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, req.Context().Err()
}
