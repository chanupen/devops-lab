// Command assist-receiver accepts Alertmanager webhooks inside the ai namespace.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"k8s.io/client-go/rest"

	"github.com/chanupen/devops-lab/internal/assist"
)

func main() {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal("in-cluster identity unavailable")
	}
	tools, err := assist.NewClient(cfg)
	if err != nil {
		log.Fatal("tool identity unavailable")
	}
	publicKey, secretKey := os.Getenv("LANGFUSE_PUBLIC_KEY"), os.Getenv("LANGFUSE_SECRET_KEY")
	runner := assist.Runner{
		Tools: tools,
		Model: assist.HTTPModel{URL: "http://ollama.ai.svc:11434", Name: "llama3.2:3b"},
		Tracer: assist.OTLPTracer{
			URL: "http://langfuse-web.ai.svc:3000", PublicKey: publicKey,
			SecretKey: secretKey,
		},
		Sensitive: []string{publicKey, secretKey},
	}
	queue := make(chan []assist.Alert, 4)
	go func() {
		for alerts := range queue {
			for _, alert := range alerts {
				note, err := runner.Run(context.Background(), alert)
				b, marshalErr := json.Marshal(note)
				if marshalErr != nil {
					log.Print("assistant note encoding failed")
					continue
				}
				log.Printf("incident note: %s", b)
				if err != nil {
					log.Print("assistant run incomplete")
				}
			}
		}
	}()
	srv := &http.Server{
		Addr: ":8080", Handler: webhook(queue), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func webhook(queue chan<- []assist.Alert) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var payload struct {
			Alerts []struct {
				Status string            `json:"status"`
				Labels map[string]string `json:"labels"`
			} `json:"alerts"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		if err := dec.Decode(&payload); err != nil || len(payload.Alerts) == 0 || len(payload.Alerts) > 20 {
			http.Error(w, "invalid webhook", http.StatusBadRequest)
			return
		}
		if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			http.Error(w, "invalid webhook", http.StatusBadRequest)
			return
		}
		alerts := make([]assist.Alert, 0, len(payload.Alerts))
		for _, a := range payload.Alerts {
			if a.Status != "firing" || a.Labels["alertname"] == "" {
				continue
			}
			alerts = append(alerts, assist.Alert{Name: a.Labels["alertname"], Status: "firing", Labels: a.Labels})
		}
		if len(alerts) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		select {
		case queue <- alerts:
			w.WriteHeader(http.StatusAccepted)
		default:
			http.Error(w, "assistant queue full", http.StatusServiceUnavailable)
		}
	})
	return mux
}
