package local

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The build pushes to the lab registry and the cluster pulls by digest.
func TestBuildSampleDB(t *testing.T) {
	repo := writeTree(t, map[string]string{"apps/sample-db/Containerfile": "FROM x", "apps/sample-db/main.go": "package main"})
	const digest = "sha256:5520475bb809c43a7210cf180ef3c0940120f0f94fc2601e49f4022838dd0392"
	tests := []struct {
		name    string
		status  int
		failOn  string
		want    string
		wantErr string
	}{
		{"built, pushed, by digest", 200, "", "lab-zot:5000/lab/sample-db@" + digest, ""},
		{"build fails", 200, "BuildImage", "", "BuildImage failed"},
		{"registry has no such tag", 404, "", "", "lab registry has no digest for sample-db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var asked string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				asked = r.Method + " " + r.URL.Path
				if tt.status == 200 {
					w.Header().Set("Docker-Content-Digest", digest)
				}
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()
			rt := newFakeRuntime()
			rt.failOn = tt.failOn
			p := testProvider(t, rt, &fakeClusters{nodes: map[string]int{}}, false)
			p.opts.RepoRoot, p.registry, p.client = repo, srv.URL, srv.Client()
			got, err := p.BuildSampleDB(context.Background(), io.Discard)
			if (tt.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) || got != tt.want {
				t.Fatalf("BuildSampleDB = %q, %v; want %q, %q", got, err, tt.want, tt.wantErr)
			}
			if err != nil {
				return
			}
			if len(rt.built) != 1 || !strings.HasPrefix(rt.built[0], "127.0.0.1:5000/lab/sample-db:") || !slices.Equal(rt.pushed, rt.built) {
				t.Errorf("built %v, pushed %v", rt.built, rt.pushed)
			}
			tag := strings.TrimPrefix(rt.built[0], "127.0.0.1:5000/lab/sample-db:")
			if asked != "HEAD /v2/lab/sample-db/manifests/"+tag {
				t.Errorf("asked the registry %q", asked)
			}
		})
	}
}

// The tag follows the sources: the same files make the same tag.
func TestDirHash(t *testing.T) {
	a := writeTree(t, map[string]string{"main.go": "package main", ".git/x": "ignored"})
	b := writeTree(t, map[string]string{"main.go": "package main"})
	c := writeTree(t, map[string]string{"main.go": "package main // changed"})
	ha, _ := dirHash(a)
	hb, _ := dirHash(b)
	hc, _ := dirHash(c)
	if ha != hb || ha == hc {
		t.Errorf("hashes: %s %s %s", ha, hb, hc)
	}
}

func TestTarDir(t *testing.T) {
	dir := writeTree(t, map[string]string{"Containerfile": "FROM x", "sub/a.go": "a", ".hidden": "no", ".git/config": "no"})
	var buf bytes.Buffer
	if err := tarDir(dir, &buf); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(&buf)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Containerfile", "sub/a.go"}) {
		t.Errorf("tar has %v", names)
	}
}

func TestPodmanBuildAndPush(t *testing.T) {
	tests := []struct {
		name    string
		routes  map[string]route
		call    func(p *Podman) error
		wantErr string
	}{
		{"build streams its log", map[string]route{"POST /build": {200, `{"stream":"STEP 1/2\n"}` + "\n" + `{"stream":"done\n"}`}},
			func(p *Podman) error {
				return p.BuildImage(context.Background(), t.TempDir(), "Containerfile", "x:1", io.Discard)
			}, ""},
		{"build error in the stream", map[string]route{"POST /build": {200, `{"stream":"STEP 1/2\n"}` + "\n" + `{"error":"go build failed"}`}},
			func(p *Podman) error {
				return p.BuildImage(context.Background(), t.TempDir(), "Containerfile", "x:1", io.Discard)
			}, "go build failed"},
		{"push", map[string]route{"POST /images/127.0.0.1:5000/lab/x:1/push": {200, `{"stream":"pushed"}`}},
			func(p *Podman) error {
				return p.PushImage(context.Background(), "127.0.0.1:5000/lab/x:1", false, io.Discard)
			}, ""},
		{"push error", map[string]route{"POST /images/127.0.0.1:5000/lab/x:1/push": {200, `{"error":"connection refused"}`}},
			func(p *Podman) error {
				return p.PushImage(context.Background(), "127.0.0.1:5000/lab/x:1", false, io.Discard)
			}, "connection refused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, seen, _ := fakePodman(t, tt.routes)
			err := tt.call(p)
			if (tt.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("err = %v, want %q (requests %v)", err, tt.wantErr, *seen)
			}
		})
	}
}
