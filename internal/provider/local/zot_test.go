package local

import (
	"encoding/json"
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
