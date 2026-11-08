package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestObserveLabRegistry(t *testing.T) {
	for _, tt := range []struct {
		name                string
		status              int
		running, hosts, lan bool
		wantErr             string
	}{
		{"ready", 200, true, true, false, ""},
		{"unavailable", 503, true, true, false, "HTTP 503"},
		{"redirect refused", 302, true, true, false, "HTTP 302"},
		{"missing mirror", 200, true, false, false, "mirror file"},
		{"stopped", 200, false, true, false, "stopped"},
		{"second binding on LAN", 200, false, true, true, "not on 127.0.0.1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				w.Header().Set("Location", "/v2/lab/doctor/blobs/uploads/test")
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()
			rt := newFakeRuntime()
			binds := []string{"127.0.0.1:5000"}
			if tt.lan {
				binds = append(binds, "0.0.0.0:5000")
			}
			rt.containers[ZotContainer] = ContainerInfo{Exists: true, Running: tt.running, HostPorts: map[string][]string{"5000/tcp": binds}}
			home := t.TempDir()
			if tt.hosts {
				if err := EnsureLabRegistry(home); err != nil {
					t.Fatal(err)
				}
			}
			err := ObserveLabRegistry(context.Background(), rt, srv.Client(), srv.URL, home)
			if tt.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err=%v", err)
			}
			for _, call := range calls {
				if call != "GET /v2/" {
					t.Fatalf("not observational: %v", calls)
				}
			}
			if tt.running && len(calls) != 1 {
				t.Fatalf("calls=%v", calls)
			}
		})
	}
}
