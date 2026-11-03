package local

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestZotConfig(t *testing.T) {
	b, err := zotConfig()
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		HTTP struct {
			Address, Port string
			Compat        []string `json:"compat"`
		} `json:"http"`
		Extensions struct {
			Sync struct {
				Enable     bool `json:"enable"`
				Registries []struct {
					URLs     []string `json:"urls"`
					OnDemand bool     `json:"onDemand"`
					Content  []struct {
						Prefix, Destination string
					} `json:"content"`
				} `json:"registries"`
			} `json:"sync"`
		} `json:"extensions"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Port != "5000" || !cfg.Extensions.Sync.Enable || len(cfg.HTTP.Compat) != 1 || cfg.HTTP.Compat[0] != "docker2s2" {
		t.Errorf("http/sync = %+v %+v", cfg.HTTP, cfg.Extensions.Sync.Enable)
	}
	want := map[string]string{ // destination -> upstream URL
		"/docker.io":       "https://registry-1.docker.io",
		"/ghcr.io":         "https://ghcr.io",
		"/quay.io":         "https://quay.io",
		"/registry.k8s.io": "https://registry.k8s.io",
	}
	got := map[string]string{}
	for _, r := range cfg.Extensions.Sync.Registries {
		if !r.OnDemand || len(r.URLs) != 1 || len(r.Content) != 1 || r.Content[0].Prefix != "**" {
			t.Errorf("registry = %+v", r)
			continue
		}
		got[r.Content[0].Destination] = r.URLs[0]
	}
	if len(got) != len(want) {
		t.Errorf("registries = %v, want %v", got, want)
	}
	for d, u := range want {
		if got[d] != u {
			t.Errorf("%s -> %q, want %q", d, got[d], u)
		}
	}
}

func TestHostsTOML(t *testing.T) {
	tests := []struct {
		up   upstream
		want []string
	}{
		{upstream{"docker.io", "https://registry-1.docker.io"}, []string{
			`server = "https://registry-1.docker.io"`,
			`[host."http://lab-zot:5000/v2/docker.io"]`,
			`capabilities = ["pull", "resolve"]`,
			`override_path = true`,
		}},
		{upstream{"registry.k8s.io", "https://registry.k8s.io"}, []string{
			`server = "https://registry.k8s.io"`,
			`[host."http://lab-zot:5000/v2/registry.k8s.io"]`,
		}},
	}
	for _, tt := range tests {
		got := hostsTOML(tt.up)
		for _, w := range tt.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s hosts.toml lacks %q:\n%s", tt.up.Name, w, got)
			}
		}
	}
}

func TestConfigHash(t *testing.T) {
	tests := []struct {
		name         string
		a1, a2       string
		c1, c2       string
		wantSameHash bool
	}{
		{"same inputs", "img", "img", "cfg", "cfg", true},
		{"image changed", "img:1", "img:2", "cfg", "cfg", false},
		{"config changed", "img", "img", "cfg1", "cfg2", false},
		{"boundary is not ambiguous", "ab", "a", "c", "bc", false},
	}
	for _, tt := range tests {
		same := configHash(tt.a1, []byte(tt.c1)) == configHash(tt.a2, []byte(tt.c2))
		if same != tt.wantSameHash {
			t.Errorf("%s: same = %v, want %v", tt.name, same, tt.wantSameHash)
		}
	}
}

// The mirror files cover every upstream and the lab registry, which the
// nodes reach over plain HTTP by name.
func TestWriteMirrorFiles(t *testing.T) {
	home := t.TempDir()
	if err := EnsureLabRegistry(home); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ dir, want string }{
		{"docker.io", "http://lab-zot:5000/v2/docker.io"},
		{"ghcr.io", "http://lab-zot:5000/v2/ghcr.io"},
		{"lab-zot:5000", "server = \"http://lab-zot:5000\""},
	}
	for _, tt := range tests {
		b, err := os.ReadFile(filepath.Join(home, "kind", "certs.d", tt.dir, "hosts.toml"))
		if err != nil || !strings.Contains(string(b), tt.want) {
			t.Errorf("%s/hosts.toml = %q, %v; want %q", tt.dir, b, err, tt.want)
		}
	}
	if LabRegistryPush != "127.0.0.1:5000/lab" || LabRegistryPull != "lab-zot:5000/lab" {
		t.Errorf("lab registry = %s, %s", LabRegistryPush, LabRegistryPull)
	}
}

func TestCheckLabRegistry(t *testing.T) {
	local := map[string][]string{"5000/tcp": {"127.0.0.1:5000"}}
	tests := []struct {
		name    string
		zot     *ContainerInfo
		status  int // the registry's answer to an upload
		hosts   bool
		wantErr string
		is      error
	}{
		{"ready", &ContainerInfo{Exists: true, Running: true, HostPorts: local}, 202, true, "", nil},
		{"no Zot yet", nil, 202, true, "is not there yet", ErrNoRegistry},
		{"stopped", &ContainerInfo{Exists: true, HostPorts: local}, 202, true, "lab-zot is stopped: podman start lab-zot", nil},
		{"no host port", &ContainerInfo{Exists: true, Running: true}, 202, true, "publishes no host port for 5000", nil},
		{"on the LAN", &ContainerInfo{Exists: true, Running: true, HostPorts: map[string][]string{"5000/tcp": {"0.0.0.0:5000"}}}, 202, true,
			"publishes 5000 on 0.0.0.0:5000, not on 127.0.0.1 only", nil},
		{"push refused", &ContainerInfo{Exists: true, Running: true, HostPorts: local}, 403, true, "answered HTTP 403 to an upload", nil},
		{"no mirror file for the nodes", &ContainerInfo{Exists: true, Running: true, HostPorts: local}, 202, false,
			"so the nodes cannot pull lab-zot:5000/lab", ErrNoPullFile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				if r.Method == http.MethodPost {
					w.Header().Set("Location", "/v2/lab/doctor/blobs/uploads/u1")
					w.WriteHeader(tt.status)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			rt := newFakeRuntime()
			if tt.zot != nil {
				rt.containers[ZotContainer] = *tt.zot
			}
			home := t.TempDir()
			if tt.hosts {
				if err := EnsureLabRegistry(home); err != nil {
					t.Fatal(err)
				}
			}
			err := CheckLabRegistry(context.Background(), rt, srv.Client(), srv.URL, home)
			if (tt.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("err %v is not %v", err, tt.is)
			}
			// An accepted upload is cancelled: nothing stays in the registry.
			if tt.status == 202 && slices.Contains(calls, "POST /v2/lab/doctor/blobs/uploads/") &&
				!slices.Contains(calls, "DELETE /v2/lab/doctor/blobs/uploads/u1") {
				t.Errorf("upload not cancelled: %v", calls)
			}
		})
	}
}
