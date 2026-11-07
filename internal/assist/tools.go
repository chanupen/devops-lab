// Package assist provides the read-only evidence tools for the assistant.
package assist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/rest"
)

const (
	maxResponse = 1 << 20
	maxQuery    = 1024
	maxItems    = 100
)

// Tools is the complete model-facing tool interface. No method accepts a URL
// or an HTTP method; Call rejects unknown names and arguments.
type Tools interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

// Client uses the dedicated read-only tool identity, never an admin identity.
// The caller must supply its restricted Kubernetes config explicitly.
type Client struct {
	base *url.URL
	http *http.Client
}

// NewClient creates a guarded client for the Kubernetes API server.
func NewClient(cfg *rest.Config) (*Client, error) {
	if cfg == nil {
		return nil, errors.New("tool identity config is required")
	}
	if (cfg.BearerToken == "" && cfg.BearerTokenFile == "") || cfg.ExecProvider != nil ||
		cfg.AuthProvider != nil || cfg.Username != "" || cfg.Password != "" ||
		cfg.CertFile != "" || len(cfg.CertData) != 0 || cfg.Transport != nil ||
		cfg.WrapTransport != nil || cfg.Dial != nil || cfg.Proxy != nil ||
		cfg.Insecure || cfg.Impersonate.UserName != "" || cfg.Impersonate.UID != "" ||
		len(cfg.Impersonate.Groups) != 0 || len(cfg.Impersonate.Extra) != 0 {
		return nil, errors.New("tool identity must use a dedicated bearer token without exec, impersonation or custom transport")
	}
	base, err := url.Parse(cfg.Host)
	if err != nil || base == nil || base.Scheme != "https" ||
		base.Host == "" || base.User != nil || (base.Path != "" && base.Path != "/") ||
		base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("invalid Kubernetes API server URL")
	}
	cfg = rest.CopyConfig(cfg)
	cfg.Proxy = func(*http.Request) (*url.URL, error) { return nil, nil }
	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("tool transport: %w", err)
	}
	return newClient(base, transport), nil
}

func newClient(base *url.URL, transport http.RoundTripper) *Client {
	return &Client{
		base: base,
		http: &http.Client{
			Transport: &guardTransport{base: base, next: transport},
			Timeout:   10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("redirects are not allowed")
			},
		},
	}
}

type args struct {
	Namespace string `json:"namespace,omitempty"`
	Pod       string `json:"pod,omitempty"`
	App       string `json:"app,omitempty"`
	Query     string `json:"query,omitempty"`
	Start     string `json:"start,omitempty"`
	End       string `json:"end,omitempty"`
}

// Call accepts only the documented tool names and their specific arguments.
func (c *Client) Call(ctx context.Context, name string, raw json.RawMessage) (result json.RawMessage, err error) {
	defer func() {
		if len(result) > maxResponse {
			result = nil
			err = errors.New("tool result exceeds size limit")
		}
	}()
	switch name {
	case "events", "pods", "nodes", "pod_describe", "loki_logs", "prometheus_query", "argocd_history":
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	if len(raw) > 4096 {
		return nil, errors.New("tool arguments exceed size limit")
	}
	var a args
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return nil, fmt.Errorf("invalid tool arguments: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid trailing tool arguments")
	}
	switch name {
	case "events":
		if !only(a, "namespace", "pod") || !validNamespace(a.Namespace) || (a.Pod != "" && !validName(a.Pod)) {
			return nil, errors.New("events requires a valid namespace and optional pod")
		}
		return c.events(ctx, a.Namespace, a.Pod)
	case "pods":
		if !only(a, "namespace", "pod") || !validNamespace(a.Namespace) || (a.Pod != "" && !validName(a.Pod)) {
			return nil, errors.New("pods requires a valid namespace and optional pod")
		}
		return c.pods(ctx, a.Namespace, a.Pod)
	case "nodes":
		if !only(a) {
			return nil, errors.New("nodes takes no arguments")
		}
		return c.nodes(ctx)
	case "pod_describe":
		if !only(a, "namespace", "pod") || !validNamespace(a.Namespace) || !validName(a.Pod) {
			return nil, errors.New("pod_describe requires a valid namespace and pod")
		}
		pod, err := c.pods(ctx, a.Namespace, a.Pod)
		if err != nil {
			return nil, err
		}
		events, err := c.events(ctx, a.Namespace, a.Pod)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Pod    json.RawMessage `json:"pod"`
			Events json.RawMessage `json:"events"`
		}{pod, events})
	case "argocd_history":
		if !only(a, "app") || !validName(a.App) {
			return nil, errors.New("argocd_history requires a valid app")
		}
		return c.history(ctx, a.App)
	case "loki_logs":
		if !only(a, "query", "start", "end") || !validQuery(a.Query) {
			return nil, errors.New("loki_logs requires a query and a bounded time range")
		}
		start, end, err := timeRange(a.Start, a.End)
		if err != nil {
			return nil, err
		}
		return c.get(ctx, "/api/v1/namespaces/monitoring/services/loki:3100/proxy/loki/api/v1/query_range",
			url.Values{"query": {a.Query}, "start": {start}, "end": {end}, "limit": {strconv.Itoa(maxItems)}})
	case "prometheus_query":
		if !only(a, "query", "end") || !validQuery(a.Query) {
			return nil, errors.New("prometheus_query requires a query and optional evaluation time")
		}
		q := url.Values{"query": {a.Query}}
		if a.End != "" {
			t, err := time.Parse(time.RFC3339Nano, a.End)
			if err != nil {
				return nil, errors.New("invalid evaluation time")
			}
			q.Set("time", t.UTC().Format(time.RFC3339Nano))
		}
		return c.get(ctx, "/api/v1/namespaces/monitoring/services/kps-prometheus:9090/proxy/api/v1/query", q)
	}
	return nil, errors.New("unknown tool")
}

func only(a args, fields ...string) bool {
	values := map[string]string{"namespace": a.Namespace, "pod": a.Pod, "app": a.App,
		"query": a.Query, "start": a.Start, "end": a.End}
	for _, field := range fields {
		delete(values, field)
	}
	for _, value := range values {
		if value != "" {
			return false
		}
	}
	return true
}

func validNamespace(s string) bool { return len(validation.IsDNS1123Label(s)) == 0 }
func validName(s string) bool      { return len(validation.IsDNS1123Subdomain(s)) == 0 }

func validQuery(s string) bool {
	return s != "" && len(s) <= maxQuery && !strings.ContainsAny(s, "\x00\r\n")
}

func timeRange(start, end string) (string, string, error) {
	s, err1 := time.Parse(time.RFC3339Nano, start)
	e, err2 := time.Parse(time.RFC3339Nano, end)
	if err1 != nil || err2 != nil || !e.After(s) || e.Sub(s) > time.Hour {
		return "", "", errors.New("time range must be valid and at most one hour")
	}
	return s.UTC().Format(time.RFC3339Nano), e.UTC().Format(time.RFC3339Nano), nil
}

type podSummary struct {
	Name                  string                   `json:"name"`
	Namespace             string                   `json:"namespace"`
	Node                  string                   `json:"node,omitempty"`
	Phase                 corev1.PodPhase          `json:"phase"`
	Conditions            []corev1.PodCondition    `json:"conditions,omitempty"`
	Containers            []containerSummary       `json:"containers,omitempty"`
	InitContainerStatuses []corev1.ContainerStatus `json:"initContainerStatuses,omitempty"`
	ContainerStatuses     []corev1.ContainerStatus `json:"containerStatuses,omitempty"`
}

type containerSummary struct {
	Name      string                      `json:"name"`
	Image     string                      `json:"image"`
	Resources corev1.ResourceRequirements `json:"resources"`
}

func summarizePod(p corev1.Pod) podSummary {
	out := podSummary{
		Name: p.Name, Namespace: p.Namespace, Node: p.Spec.NodeName, Phase: p.Status.Phase,
		Conditions: p.Status.Conditions, InitContainerStatuses: p.Status.InitContainerStatuses,
		ContainerStatuses: p.Status.ContainerStatuses,
	}
	for _, c := range p.Spec.Containers {
		out.Containers = append(out.Containers, containerSummary{Name: c.Name, Image: c.Image, Resources: c.Resources})
	}
	return out
}

func (c *Client) nodes(ctx context.Context) (json.RawMessage, error) {
	body, err := c.get(ctx, "/api/v1/nodes", url.Values{"limit": {strconv.Itoa(maxItems)}})
	if err != nil {
		return nil, err
	}
	var list corev1.NodeList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decode nodes: %w", err)
	}
	type nodeSummary struct {
		Name       string                 `json:"name"`
		Conditions []corev1.NodeCondition `json:"conditions"`
	}
	items := make([]nodeSummary, 0, len(list.Items))
	for _, node := range list.Items {
		items = append(items, nodeSummary{Name: node.Name, Conditions: node.Status.Conditions})
	}
	return json.Marshal(struct {
		Items     []nodeSummary `json:"items"`
		Truncated bool          `json:"truncated"`
	}{items, list.Continue != ""})
}

func (c *Client) pods(ctx context.Context, ns, pod string) (json.RawMessage, error) {
	path := "/api/v1/namespaces/" + ns + "/pods"
	q := url.Values{"limit": {strconv.Itoa(maxItems)}}
	if pod != "" {
		path += "/" + pod
		q = nil
	}
	body, err := c.get(ctx, path, q)
	if err != nil {
		return nil, err
	}
	if pod != "" {
		var p corev1.Pod
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, fmt.Errorf("decode pod: %w", err)
		}
		return json.Marshal(summarizePod(p))
	}
	var list corev1.PodList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decode pods: %w", err)
	}
	items := make([]podSummary, 0, len(list.Items))
	for _, p := range list.Items {
		items = append(items, summarizePod(p))
	}
	return json.Marshal(struct {
		Items     []podSummary `json:"items"`
		Truncated bool         `json:"truncated"`
	}{items, list.Continue != ""})
}

type eventSummary struct {
	Reason    string                 `json:"reason"`
	Message   string                 `json:"message"`
	Type      string                 `json:"type"`
	Involved  corev1.ObjectReference `json:"involvedObject"`
	Count     int32                  `json:"count"`
	FirstSeen metav1.Time            `json:"firstSeen"`
	LastSeen  metav1.Time            `json:"lastSeen"`
}

func (c *Client) events(ctx context.Context, ns, pod string) (json.RawMessage, error) {
	q := url.Values{"limit": {strconv.Itoa(maxItems)}}
	if pod != "" {
		q.Set("fieldSelector", "involvedObject.kind=Pod,involvedObject.name="+pod)
	}
	body, err := c.get(ctx, "/api/v1/namespaces/"+ns+"/events", q)
	if err != nil {
		return nil, err
	}
	var list corev1.EventList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}
	items := make([]eventSummary, 0, len(list.Items))
	for _, e := range list.Items {
		items = append(items, eventSummary{e.Reason, e.Message, e.Type, e.InvolvedObject, e.Count, e.FirstTimestamp, e.LastTimestamp})
	}
	return json.Marshal(struct {
		Items     []eventSummary `json:"items"`
		Truncated bool           `json:"truncated"`
	}{items, list.Continue != ""})
}

func (c *Client) history(ctx context.Context, app string) (json.RawMessage, error) {
	body, err := c.get(ctx, "/apis/argoproj.io/v1alpha1/namespaces/argocd/applications/"+app, nil)
	if err != nil {
		return nil, err
	}
	var obj struct {
		Status struct {
			History []struct {
				ID         int64       `json:"id"`
				Revision   string      `json:"revision"`
				DeployedAt metav1.Time `json:"deployedAt"`
			} `json:"history"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("decode application: %w", err)
	}
	return json.Marshal(obj.Status.History)
}

func (c *Client) get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	u := *c.base
	u.Path = path
	u.RawQuery = query.Encode()
	if !allowedPath(&u) {
		return nil, errors.New("tool endpoint denied")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tool request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tool request: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read tool response: %w", err)
	}
	if len(body) > maxResponse {
		return nil, errors.New("tool response exceeds size limit")
	}
	if !json.Valid(body) {
		return nil, errors.New("tool response is not JSON")
	}
	return body, nil
}
