package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// apiPrefix is the libpod API version path. Podman 5 and 6 both serve it.
const apiPrefix = "/v5.0.0/libpod"

// Podman talks to the rootless Podman REST API over its unix socket.
type Podman struct {
	base   string // scheme and host; the socket ignores the host
	client *http.Client
}

// PodmanSocket returns the rootless socket path: CONTAINER_HOST if it is a
// unix:// URL, else $XDG_RUNTIME_DIR/podman/podman.sock.
func PodmanSocket() (string, error) {
	if h := os.Getenv("CONTAINER_HOST"); strings.HasPrefix(h, "unix://") {
		return strings.TrimPrefix(h, "unix://"), nil
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return "", errors.New("XDG_RUNTIME_DIR is not set; is this a login session with podman.socket?")
	}
	return filepath.Join(dir, "podman", "podman.sock"), nil
}

// NewPodman returns a client for the socket at path.
func NewPodman(socket string) *Podman {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	return &Podman{base: "http://podman", client: &http.Client{Transport: tr}}
}

// newPodmanHTTP points the client at a plain HTTP server (tests).
func newPodmanHTTP(base string, c *http.Client) *Podman { return &Podman{base: base, client: c} }

// apiError is libpod's error body.
type apiError struct {
	Message  string `json:"message"`
	Response int    `json:"response"`
}

// do sends a request and returns the response for statuses in ok; other
// statuses become errors carrying libpod's message.
func (p *Podman) do(ctx context.Context, method, path string, body any, ok ...int) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+apiPrefix+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("podman API: %w", err)
	}
	for _, c := range ok {
		if resp.StatusCode == c {
			return resp, nil
		}
	}
	defer func() { _ = resp.Body.Close() }()
	var e apiError
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if json.Unmarshal(b, &e) == nil && e.Message != "" {
		return nil, fmt.Errorf("podman %s %s: %s (HTTP %d)", method, path, e.Message, resp.StatusCode)
	}
	return nil, fmt.Errorf("podman %s %s: HTTP %d", method, path, resp.StatusCode)
}

// exists maps 204 to true and 404 to false.
func (p *Podman) exists(ctx context.Context, path string) (bool, error) {
	resp, err := p.do(ctx, http.MethodGet, path, nil, http.StatusNoContent, http.StatusNotFound)
	if err != nil {
		return false, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent, nil
}

// NetworkExists implements Runtime.
func (p *Podman) NetworkExists(ctx context.Context, name string) (bool, error) {
	return p.exists(ctx, "/networks/"+url.PathEscape(name)+"/exists")
}

// CreateNetwork implements Runtime.
func (p *Podman) CreateNetwork(ctx context.Context, n NetworkSpec) error {
	body := map[string]any{"name": n.Name, "driver": "bridge", "dns_enabled": true}
	if n.IPv6Subnet != "" {
		body["ipv6_enabled"] = true
		body["subnets"] = []map[string]string{{"subnet": n.IPv6Subnet}}
	}
	resp, err := p.do(ctx, http.MethodPost, "/networks/create", body, http.StatusOK, http.StatusCreated)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// PullImage implements Runtime. Podman streams JSON progress; an "error"
// field anywhere in the stream fails the pull.
func (p *Podman) PullImage(ctx context.Context, ref string) error {
	q := url.Values{"reference": {ref}, "policy": {"missing"}, "quiet": {"true"}}
	resp, err := p.do(ctx, http.MethodPost, "/images/pull?"+q.Encode(), nil, http.StatusOK)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return fmt.Errorf("pull %s: %w", ref, err)
		}
		if msg.Error != "" {
			return fmt.Errorf("pull %s: %s", ref, msg.Error)
		}
	}
}

// Container implements Runtime.
func (p *Podman) Container(ctx context.Context, name string) (ContainerInfo, error) {
	resp, err := p.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", nil, http.StatusOK, http.StatusNotFound)
	if err != nil {
		return ContainerInfo{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return ContainerInfo{}, nil
	}
	var d struct {
		ImageName string `json:"ImageName"`
		State     struct {
			Running bool `json:"Running"`
		} `json:"State"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return ContainerInfo{}, fmt.Errorf("inspect %s: %w", name, err)
	}
	return ContainerInfo{Exists: true, Running: d.State.Running, Image: d.ImageName, Labels: d.Config.Labels}, nil
}

// createBody maps a ContainerSpec to libpod's SpecGenerator JSON.
func createBody(c ContainerSpec) map[string]any {
	ports := []map[string]any{}
	for _, pt := range c.Ports {
		ports = append(ports, map[string]any{
			"host_ip": pt.HostIP, "host_port": pt.HostPort, "container_port": pt.ContainerPort, "protocol": "tcp",
		})
	}
	vols := []map[string]any{}
	for _, v := range c.Volumes {
		vols = append(vols, map[string]any{"Name": v.Name, "Dest": v.Dest})
	}
	mounts := []map[string]any{}
	for _, b := range c.Binds {
		opts := []string{"Z"} // private SELinux label for this container
		if b.ReadOnly {
			opts = append(opts, "ro")
		}
		mounts = append(mounts, map[string]any{"destination": b.Dest, "type": "bind", "source": b.Source, "options": opts})
	}
	body := map[string]any{
		"name": c.Name, "image": c.Image, "labels": c.Labels,
		"portmappings": ports, "volumes": vols, "mounts": mounts,
	}
	if c.Network != "" {
		body["netns"] = map[string]string{"nsmode": "bridge"}
		body["Networks"] = map[string]any{c.Network: map[string]any{"aliases": c.Aliases}}
	}
	return body
}

// CreateContainer implements Runtime.
func (p *Podman) CreateContainer(ctx context.Context, c ContainerSpec) error {
	resp, err := p.do(ctx, http.MethodPost, "/containers/create", createBody(c), http.StatusCreated)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// StartContainer implements Runtime. Starting a running container is not an error.
func (p *Podman) StartContainer(ctx context.Context, name string) error {
	resp, err := p.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/start", nil,
		http.StatusNoContent, http.StatusNotModified)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// RemoveContainer implements Runtime.
func (p *Podman) RemoveContainer(ctx context.Context, name string) error {
	resp, err := p.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(name)+"?force=true", nil,
		http.StatusOK, http.StatusNoContent, http.StatusNotFound)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// RemoveVolume implements Runtime.
func (p *Podman) RemoveVolume(ctx context.Context, name string) error {
	resp, err := p.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), nil,
		http.StatusNoContent, http.StatusNotFound)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
