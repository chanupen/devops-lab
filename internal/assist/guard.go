package assist

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type guardTransport struct {
	base *url.URL
	next http.RoundTripper
}

func (g *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL
	if req.Method != http.MethodGet || req.Body != nil || req.ContentLength != 0 ||
		len(req.TransferEncoding) != 0 || len(req.Trailer) != 0 || len(req.Header) != 0 || u == nil ||
		u.Scheme != g.base.Scheme || u.Host != g.base.Host || u.User != nil ||
		u.Opaque != "" || u.Fragment != "" || u.ForceQuery || u.RawPath != "" ||
		u.EscapedPath() != u.Path || (req.Host != "" && req.Host != g.base.Host) || !allowedPath(u) {
		return nil, errors.New("tool request denied")
	}
	return g.next.RoundTrip(req)
}

func allowedPath(u *url.URL) bool {
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) >= 6 && parts[0] == "" && parts[1] == "api" &&
		parts[2] == "v1" && parts[3] == "namespaces" && validNamespace(parts[4]) {
		ns := parts[4]
		switch {
		case len(parts) == 6 && parts[5] == "pods":
			return exactQuery(q, "limit") && q.Get("limit") == "100"
		case len(parts) == 7 && parts[5] == "pods" && validName(parts[6]):
			return len(q) == 0
		case len(parts) == 6 && parts[5] == "events":
			if !exactQuery(q, "limit") && !exactQuery(q, "limit", "fieldSelector") {
				return false
			}
			selector := q.Get("fieldSelector")
			const prefix = "involvedObject.kind=Pod,involvedObject.name="
			return q.Get("limit") == "100" &&
				(!q.Has("fieldSelector") || (strings.HasPrefix(selector, prefix) && validName(strings.TrimPrefix(selector, prefix))))
		case ns == "monitoring" && u.Path == "/api/v1/namespaces/monitoring/services/loki:3100/proxy/loki/api/v1/query_range":
			if !exactQuery(q, "query", "start", "end", "limit") || !validQuery(q.Get("query")) || q.Get("limit") != "100" {
				return false
			}
			_, _, err := timeRange(q.Get("start"), q.Get("end"))
			return err == nil
		case ns == "monitoring" && u.Path == "/api/v1/namespaces/monitoring/services/kps-prometheus:9090/proxy/api/v1/query":
			if (!exactQuery(q, "query") && !exactQuery(q, "query", "time")) || !validQuery(q.Get("query")) {
				return false
			}
			if q.Has("time") {
				_, err := time.Parse(time.RFC3339Nano, q.Get("time"))
				return err == nil
			}
			return true
		}
	}
	return len(parts) == 8 && parts[0] == "" && parts[1] == "apis" &&
		parts[2] == "argoproj.io" && parts[3] == "v1alpha1" &&
		parts[4] == "namespaces" && parts[5] == "argocd" &&
		parts[6] == "applications" && validName(parts[7]) && len(q) == 0
}

func exactQuery(q url.Values, keys ...string) bool {
	if len(q) != len(keys) {
		return false
	}
	for _, k := range keys {
		if len(q[k]) != 1 {
			return false
		}
	}
	return true
}
