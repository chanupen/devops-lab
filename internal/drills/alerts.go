package drills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

// Alerts tells when an alert started firing.
type Alerts interface {
	// FiredAt returns the earliest time since since that alert a, with its
	// labels, was firing; ok is false when it has not fired.
	FiredAt(ctx context.Context, a Alert, since time.Time) (at time.Time, ok bool, err error)
}

// Where the obs stack runs Alertmanager and Prometheus (kube-prometheus-
// stack's fullnameOverride is kps).
const (
	ObsNamespace = "monitoring"
	Alertmanager = "kps-alertmanager:9093"
	Prometheus   = "kps-prometheus:9090"
)

// ProxyAlerts asks the obs stack through the API server's service proxy,
// so it needs neither a port-forward nor cluster DNS (the dns drill takes
// that away). Alertmanager answers for alerts firing now, with the time
// they started; Prometheus's ALERTS series answers for an alert that fired
// and resolved before lab looked.
type ProxyAlerts struct {
	REST rest.Interface // a core/v1 REST client of the main cluster
}

// FiredAt implements Alerts.
func (p ProxyAlerts) FiredAt(ctx context.Context, a Alert, since time.Time) (time.Time, bool, error) {
	at, ok, amErr := p.alertmanager(ctx, a, since)
	if ok {
		return at, true, nil
	}
	at, ok, promErr := p.prometheus(ctx, a, since)
	if ok || promErr == nil {
		return at, ok, nil
	}
	if amErr != nil {
		return time.Time{}, false, fmt.Errorf("alertmanager: %w; prometheus: %w", amErr, promErr)
	}
	return time.Time{}, false, promErr
}

func (p ProxyAlerts) proxy(svc, path string, query url.Values) *rest.Request {
	r := p.REST.Get().AbsPath("/api/v1/namespaces/" + ObsNamespace + "/services/" + svc + "/proxy" + path)
	for k, vs := range query {
		for _, v := range vs {
			r = r.Param(k, v)
		}
	}
	return r
}

func (p ProxyAlerts) alertmanager(ctx context.Context, a Alert, since time.Time) (time.Time, bool, error) {
	q := url.Values{"active": {"true"}, "silenced": {"true"}, "inhibited": {"true"}}
	q.Add("filter", fmt.Sprintf("alertname=%q", a.Name))
	for _, k := range sortedKeys(a.Labels) {
		q.Add("filter", fmt.Sprintf("%s=%q", k, a.Labels[k]))
	}
	b, err := p.proxy(Alertmanager, "/api/v2/alerts", q).DoRaw(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	return earliestAlert(b, since)
}

// earliestAlert reads Alertmanager's /api/v2/alerts and returns the
// earliest startsAt at or after since.
func earliestAlert(b []byte, since time.Time) (time.Time, bool, error) {
	var alerts []struct {
		StartsAt time.Time `json:"startsAt"`
	}
	if err := json.Unmarshal(b, &alerts); err != nil {
		return time.Time{}, false, err
	}
	var first time.Time
	for _, al := range alerts {
		if al.StartsAt.Before(since) {
			continue // firing before the drill: not its doing
		}
		if first.IsZero() || al.StartsAt.Before(first) {
			first = al.StartsAt
		}
	}
	return first, !first.IsZero(), nil
}

func (p ProxyAlerts) prometheus(ctx context.Context, a Alert, since time.Time) (time.Time, bool, error) {
	sel := []string{fmt.Sprintf("alertname=%q", a.Name), `alertstate="firing"`}
	for _, k := range sortedKeys(a.Labels) {
		sel = append(sel, fmt.Sprintf("%s=%q", k, a.Labels[k]))
	}
	now := time.Now()
	q := url.Values{
		"query": {"ALERTS{" + strings.Join(sel, ",") + "}"},
		"start": {strconv.FormatInt(since.Unix(), 10)},
		"end":   {strconv.FormatInt(now.Unix(), 10)},
		"step":  {"15"},
	}
	b, err := p.proxy(Prometheus, "/api/v1/query_range", q).DoRaw(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	return firstSample(b)
}

// firstSample reads a Prometheus range query and returns its earliest sample.
func firstSample(b []byte) (time.Time, bool, error) {
	var r struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Values [][2]any `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return time.Time{}, false, err
	}
	if r.Status != "success" {
		return time.Time{}, false, fmt.Errorf("prometheus: status %q", r.Status)
	}
	var first float64
	for _, s := range r.Data.Result {
		if len(s.Values) == 0 {
			continue
		}
		if t, ok := s.Values[0][0].(float64); ok && (first == 0 || t < first) {
			first = t
		}
	}
	if first == 0 {
		return time.Time{}, false, nil
	}
	return time.Unix(int64(first), 0), true, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
