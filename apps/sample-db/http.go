package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics are the app's Prometheus metrics, on their own registry.
type Metrics struct {
	reg        *prometheus.Registry
	requests   *prometheus.CounterVec
	dbErrors   prometheus.Counter
	reconnects *prometheus.CounterVec
	reloads    prometheus.Counter
	user       *prometheus.GaugeVec
	mu         sync.Mutex
	lastUser   string
}

func newMetrics() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sampledb_http_requests_total", Help: "HTTP requests by path and status code.",
		}, []string{"path", "code"}),
		dbErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sampledb_db_errors_total", Help: "Database queries that failed.",
		}),
		reconnects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sampledb_reconnects_total", Help: "New connection pools opened, by result.",
		}, []string{"result"}),
		reloads: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sampledb_credential_reloads_total", Help: "Times the app moved to new credentials without restarting.",
		}),
		user: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "sampledb_db_user", Help: "1 for the database user the app connects as.",
		}, []string{"user"}),
	}
	m.reg.MustRegister(m.requests, m.dbErrors, m.reconnects, m.reloads, m.user)
	return m
}

func (m *Metrics) setUser(u string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastUser != "" && m.lastUser != u {
		m.user.DeleteLabelValues(m.lastUser)
	}
	m.user.WithLabelValues(u).Set(1)
	m.lastUser = u
}

// newHandler serves /healthz (the process runs), /readyz (the database
// answers), /api/check (a query: which user the app connects as) and
// /metrics.
func newHandler(db *DB, m *Metrics) http.Handler {
	mux := http.NewServeMux()
	count := func(path string, h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			rw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
			h(rw, r)
			m.requests.WithLabelValues(path, strconv.Itoa(rw.code)).Inc()
		}
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		p, err := db.Current()
		if err == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			err = p.Ping(ctx)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/api/check", count("/api/check", func(w http.ResponseWriter, r *http.Request) {
		p, err := db.Current()
		var user string
		if err == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			user, err = p.CurrentUser(ctx)
		}
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			m.dbErrors.Inc()
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"user": user})
	}))
	mux.Handle("/metrics", promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{}))
	return mux
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}
