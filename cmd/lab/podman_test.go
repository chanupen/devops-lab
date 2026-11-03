package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeContainer is one container the fake Podman API knows.
type fakeContainer struct {
	Running    bool
	StartedAt  time.Time
	FinishedAt time.Time
}

// fakePodman serves the part of the libpod API lab reads and records the
// containers it is asked to start.
type fakePodman struct {
	mu         sync.Mutex
	containers map[string]fakeContainer
	started    []string
}

func (f *fakePodman) Started() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.started...)
}

// fakePodmanSocket serves f on a unix socket and points CONTAINER_HOST at
// it for the test.
func fakePodmanSocket(t *testing.T, containers map[string]fakeContainer) *fakePodman {
	t.Helper()
	f := &fakePodman{containers: containers}
	// A short path: unix socket paths are limited to about 100 bytes.
	dir, err := os.MkdirTemp("", "lab-podman-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "p.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(f.serve), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	t.Setenv("CONTAINER_HOST", "unix://"+sock)
	return f
}

func (f *fakePodman) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path[strings.Index(r.URL.Path, "/libpod")+len("/libpod"):]
	switch {
	case r.Method == http.MethodGet && path == "/containers/json":
		var list []map[string]any
		for name, c := range f.containers {
			state := "exited"
			if c.Running {
				state = "running"
			}
			list = append(list, map[string]any{"Names": []string{name}, "State": state,
				"StartedAt": c.StartedAt.Unix(), "ExitedAt": c.FinishedAt.Unix()})
		}
		_ = json.NewEncoder(w).Encode(list)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/json"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
		c, ok := f.containers[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"no such container"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"State": map[string]any{
			"Running": c.Running, "StartedAt": c.StartedAt, "FinishedAt": c.FinishedAt}})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/start"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/start")
		c := f.containers[name]
		c.Running, c.StartedAt = true, time.Now()
		f.containers[name] = c
		f.started = append(f.started, name)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}
