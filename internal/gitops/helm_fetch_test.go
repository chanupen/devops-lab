package gitops

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChartBackoff(t *testing.T) {
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute, time.Minute}
	for i, w := range want {
		if got := chartBackoff(i + 1); got != w {
			t.Errorf("retry %d: got %s, want %s", i+1, got, w)
		}
	}
	if got := chartBackoff(80); got != chartMaxBackoff {
		t.Errorf("retry 80 (shift overflow): got %s", got)
	}
}

func TestFetchRetriesThenCaches(t *testing.T) {
	r := HelmRelease{Chart: "argo-cd", Version: "1.2.3"}
	tests := []struct {
		name     string
		failures int // locate fails this many times first
		wantErr  string
		wantLog  []string
	}{
		{"first try", 0, "", []string{"fetched chart argo-cd 1.2.3"}},
		{"slow CDN, third try", 2, "", []string{"attempt 1 of 6: timeout; retrying in", "attempt 2 of 6", "fetched chart"}},
		{"never answers", 99, "fetch chart argo-cd 1.2.3 after 6 attempts: timeout", []string{"attempt 5 of 6"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "download.tgz")
			if err := os.WriteFile(src, []byte("chart"), 0o600); err != nil {
				t.Fatal(err)
			}
			charts := filepath.Join(dir, "shared")
			h := &sdkHelm{home: filepath.Join(dir, "home"), charts: charts, backoff: func(int) time.Duration { return 0 }}
			calls := 0
			locate := func() (string, error) {
				calls++
				if calls <= tt.failures {
					return "", errors.New("timeout")
				}
				return src, nil
			}
			var log bytes.Buffer
			p, err := h.fetch(context.Background(), r, &log, locate)
			for _, w := range tt.wantLog {
				if !strings.Contains(log.String(), w) {
					t.Errorf("log lacks %q:\n%s", w, log.String())
				}
			}
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err %v, want %q", err, tt.wantErr)
				}
				if calls != chartAttempts {
					t.Errorf("locate called %d times, want %d", calls, chartAttempts)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(charts, "argo-cd-1.2.3.tgz"); p != want {
				t.Fatalf("path %s, want the shared cache %s", p, want)
			}
			// The shared cache serves the next fetch with no network.
			before := calls
			if _, err := h.fetch(context.Background(), r, &log, locate); err != nil || calls != before {
				t.Errorf("second fetch: err %v, locate calls %d -> %d", err, before, calls)
			}
		})
	}
}

func TestFetchStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &sdkHelm{charts: t.TempDir(), backoff: func(int) time.Duration { cancel(); return time.Hour }}
	_, err := h.fetch(ctx, HelmRelease{Chart: "x", Version: "1"}, &bytes.Buffer{}, func() (string, error) { return "", errors.New("down") })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want context.Canceled", err)
	}
}
