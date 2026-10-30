package gitops

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCachedChart(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "argo-cd-10.10.5.tgz"), []byte("tgz"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty-1.0.0.tgz"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		chart, version string
		want           bool
	}{
		{"argo-cd", "10.10.5", true},
		{"argo-cd", "10.10.6", false}, // another version is fetched
		{"loki", "7.3.0", false},
		{"empty", "1.0.0", false}, // a truncated download is not trusted
	}
	for _, tt := range tests {
		p, ok := cachedChart(dir, tt.chart, tt.version)
		if ok != tt.want || (ok && p != filepath.Join(dir, tt.chart+"-"+tt.version+".tgz")) {
			t.Errorf("cachedChart(%s, %s) = %q, %v; want %v", tt.chart, tt.version, p, ok, tt.want)
		}
	}
}

func TestKeepChart(t *testing.T) {
	src := filepath.Join(t.TempDir(), "sha256-abc.tgz")
	if err := os.WriteFile(src, []byte("chart bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "charts")
	got := keepChart(src, dir, "argo-cd", "10.10.5")
	if got != filepath.Join(dir, "argo-cd-10.10.5.tgz") {
		t.Fatalf("path = %s", got)
	}
	if p, ok := cachedChart(dir, "argo-cd", "10.10.5"); !ok || p != got {
		t.Errorf("kept chart not found in cache: %q %v", p, ok)
	}
	if got := keepChart(filepath.Join(dir, "missing.tgz"), dir, "x", "1"); got != filepath.Join(dir, "missing.tgz") {
		t.Errorf("unreadable source should be returned as is, got %s", got)
	}
}
