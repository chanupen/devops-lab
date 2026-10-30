package local

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Zot runs as a rootless Podman container on the kind network, so nodes reach
// it by name, and publishes its port on 127.0.0.1 for the host.
const (
	// ZotImage is v2.1.22 (full build, with the sync extension), pinned by digest.
	ZotImage     = "ghcr.io/project-zot/zot-linux-amd64:v2.1.22@sha256:46f688dc26315a35a247e1784368829e66a67bf91de94c7d8d1645044fac1d5b"
	ZotContainer = "lab-zot"
	ZotVolume    = "lab-zot-data" // image cache; kept by lab down unless --purge
	ZotPort      = 5000
)

// upstream is a registry Zot caches on demand. Images are stored under
// /<Name>/..., so docker.io/library/nginx lives at lab-zot:5000/docker.io/library/nginx.
type upstream struct {
	Name string // registry host as written in image references
	URL  string // where to fetch from
}

// upstreams are the registries the cluster pulls through Zot.
func upstreams() []upstream {
	return []upstream{
		{"docker.io", "https://registry-1.docker.io"},
		{"ghcr.io", "https://ghcr.io"},
		{"quay.io", "https://quay.io"},
		{"registry.k8s.io", "https://registry.k8s.io"},
	}
}

// zotConfig returns Zot's config.json: local storage plus an on-demand sync
// entry per upstream, each stored under its own prefix.
func zotConfig() ([]byte, error) {
	type content struct {
		Prefix      string `json:"prefix"`
		Destination string `json:"destination"`
	}
	type registry struct {
		URLs       []string  `json:"urls"`
		OnDemand   bool      `json:"onDemand"`
		TLSVerify  bool      `json:"tlsVerify"`
		MaxRetries int       `json:"maxRetries"`
		RetryDelay string    `json:"retryDelay"`
		Content    []content `json:"content"`
	}
	var regs []registry
	for _, u := range upstreams() {
		regs = append(regs, registry{
			URLs: []string{u.URL}, OnDemand: true, TLSVerify: true, MaxRetries: 3, RetryDelay: "10s",
			Content: []content{{Prefix: "**", Destination: "/" + u.Name}},
		})
	}
	cfg := map[string]any{
		"distSpecVersion": "1.1.1",
		"storage":         map[string]any{"rootDirectory": "/var/lib/registry", "gc": true, "dedupe": true},
		// docker2s2 serves Docker v2 manifests and lists, which many upstream images still use.
		"http":       map[string]any{"address": "0.0.0.0", "port": fmt.Sprint(ZotPort), "compat": []string{"docker2s2"}},
		"log":        map[string]any{"level": "info"},
		"extensions": map[string]any{"sync": map[string]any{"enable": true, "registries": regs}},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// hostsTOML returns containerd's hosts.toml for one upstream: pull and resolve
// through Zot, falling back to the upstream itself if Zot is unreachable.
func hostsTOML(u upstream) string {
	return fmt.Sprintf(`server = %q

[host."http://%s:%d/v2/%s"]
  capabilities = ["pull", "resolve"]
  override_path = true
`, u.URL, ZotContainer, ZotPort, u.Name)
}

// configHash identifies the image and config a Zot container was created
// with, so a changed pin or config recreates the container.
func configHash(image string, config []byte) string {
	h := sha256.New()
	h.Write([]byte(image))
	h.Write([]byte{0})
	h.Write(config)
	return hex.EncodeToString(h.Sum(nil))[:16]
}
