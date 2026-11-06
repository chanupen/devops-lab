package local

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
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
			Running    bool      `json:"Running"`
			StartedAt  time.Time `json:"StartedAt"`
			FinishedAt time.Time `json:"FinishedAt"`
		} `json:"State"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		NetworkSettings struct {
			Ports map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			} `json:"Ports"`
			Networks map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return ContainerInfo{}, fmt.Errorf("inspect %s: %w", name, err)
	}
	ports := map[string][]string{}
	for p, binds := range d.NetworkSettings.Ports {
		for _, b := range binds {
			ports[p] = append(ports[p], net.JoinHostPort(b.HostIP, b.HostPort))
		}
	}
	nets := map[string]string{}
	for n, s := range d.NetworkSettings.Networks {
		nets[n] = s.IPAddress
	}
	return ContainerInfo{Name: name, Exists: true, Running: d.State.Running, Image: d.ImageName, Labels: d.Config.Labels,
		StartedAt: d.State.StartedAt, FinishedAt: d.State.FinishedAt, HostPorts: ports, Networks: nets}, nil
}

// Containers implements Runtime. libpod's list gives the times as Unix
// seconds; 0 (or less) means never.
func (p *Podman) Containers(ctx context.Context, prefix string) ([]ContainerInfo, error) {
	q := url.Values{"all": {"true"}}
	resp, err := p.do(ctx, http.MethodGet, "/containers/json?"+q.Encode(), nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var list []struct {
		Names     []string          `json:"Names"`
		Image     string            `json:"Image"`
		State     string            `json:"State"`
		Labels    map[string]string `json:"Labels"`
		StartedAt int64             `json:"StartedAt"`
		ExitedAt  int64             `json:"ExitedAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	unix := func(s int64) time.Time {
		if s <= 0 {
			return time.Time{}
		}
		return time.Unix(s, 0)
	}
	var out []ContainerInfo
	for _, c := range list {
		if len(c.Names) == 0 || !strings.HasPrefix(c.Names[0], prefix) {
			continue
		}
		out = append(out, ContainerInfo{Name: c.Names[0], Exists: true, Running: c.State == "running", Image: c.Image,
			Labels: c.Labels, StartedAt: unix(c.StartedAt), FinishedAt: unix(c.ExitedAt)})
	}
	slices.SortFunc(out, func(a, b ContainerInfo) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
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
	if len(c.HostAdd) > 0 {
		body["hostadd"] = c.HostAdd
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

// Exec implements Runtime with libpod's exec API: create the session, start
// it attached (the response is the multiplexed stdout/stderr stream), then
// read its exit code.
func (p *Podman) Exec(ctx context.Context, name string, cmd []string) (string, error) {
	resp, err := p.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/exec",
		map[string]any{"AttachStdout": true, "AttachStderr": true, "Cmd": cmd}, http.StatusCreated, http.StatusOK)
	if err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"Id"`
	}
	err = json.NewDecoder(resp.Body).Decode(&created)
	_ = resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("exec in %s: %w", name, err)
	}
	if created.ID == "" {
		return "", fmt.Errorf("exec in %s: no session id", name)
	}
	resp, err = p.do(ctx, http.MethodPost, "/exec/"+url.PathEscape(created.ID)+"/start",
		map[string]any{"Detach": false, "Tty": false}, http.StatusOK)
	if err != nil {
		return "", err
	}
	stdout, stderr, err := demux(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("exec in %s: %w", name, err)
	}
	resp, err = p.do(ctx, http.MethodGet, "/exec/"+url.PathEscape(created.ID)+"/json", nil, http.StatusOK)
	if err != nil {
		return "", err
	}
	var inspect struct {
		ExitCode int  `json:"ExitCode"`
		Running  bool `json:"Running"`
	}
	err = json.NewDecoder(resp.Body).Decode(&inspect)
	_ = resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("exec in %s: %w", name, err)
	}
	if inspect.ExitCode != 0 {
		return stdout, fmt.Errorf("%s in %s exited %d: %s", strings.Join(cmd, " "), name, inspect.ExitCode, strings.TrimSpace(stderr))
	}
	return stdout, nil
}

// demux splits Docker's multiplexed stream: frames of an 8-byte header
// (stream 1 stdout, 2 stderr; big-endian length in bytes 4 to 7) and the
// payload.
func demux(r io.Reader) (stdout, stderr string, err error) {
	var out, errb strings.Builder
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, hdr); errors.Is(err, io.EOF) {
			return out.String(), errb.String(), nil
		} else if err != nil {
			return out.String(), errb.String(), fmt.Errorf("read stream: %w", err)
		}
		n := int64(hdr[4])<<24 | int64(hdr[5])<<16 | int64(hdr[6])<<8 | int64(hdr[7])
		w := &out
		if hdr[0] == 2 {
			w = &errb
		}
		if _, err := io.CopyN(w, r, n); err != nil {
			return out.String(), errb.String(), fmt.Errorf("read stream: %w", err)
		}
	}
}

// BuildImage implements Runtime with libpod's build API: the context goes
// up as a tar, the build log streams back as JSON lines.
func (p *Podman) BuildImage(ctx context.Context, dir, containerfile, tag string, log io.Writer) error {
	return p.BuildImageFor(ctx, dir, containerfile, tag, "", log)
}

// BuildImageFor builds like BuildImage, for platform (e.g. linux/arm64; ""
// is the host's). A Containerfile whose build stage runs on $BUILDPLATFORM
// cross-compiles, so another arch needs no emulation.
func (p *Podman) BuildImageFor(ctx context.Context, dir, containerfile, tag, platform string, log io.Writer) error {
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tarDir(dir, pw)) }()
	q := url.Values{"dockerfile": {containerfile}, "t": {tag}, "timestamp": {"0"}, "layers": {"true"}}
	if platform != "" {
		q.Set("platform", platform)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+apiPrefix+"/build?"+q.Encode(), pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("podman API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("build %s: HTTP %d: %s", tag, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return streamLog(resp.Body, "build "+tag, log)
}

// PushImage implements Runtime.
func (p *Podman) PushImage(ctx context.Context, name string, tls bool, log io.Writer) error {
	q := url.Values{"tlsVerify": {fmt.Sprint(tls)}}
	resp, err := p.do(ctx, http.MethodPost, "/images/"+url.PathEscape(name)+"/push?"+q.Encode(), nil, http.StatusOK)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return streamLog(resp.Body, "push "+name, log)
}

// PushImageAuth pushes name over TLS with a registry login (ECR's token in
// cloud mode). The login goes in the request header only; nothing is
// stored in Podman's auth file.
func (p *Podman) PushImageAuth(ctx context.Context, name, username, password string, log io.Writer) error {
	auth, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return err
	}
	q := url.Values{"tlsVerify": {"true"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+apiPrefix+"/images/"+url.PathEscape(name)+"/push?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Registry-Auth", base64.URLEncoding.EncodeToString(auth))
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("podman API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("push %s: HTTP %d: %s", name, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return streamLog(resp.Body, "push "+name, log)
}

// streamLog copies a libpod JSON stream's "stream" lines to log and fails
// on an "error" (or "errorDetail") line.
func streamLog(r io.Reader, what string, log io.Writer) error {
	dec := json.NewDecoder(r)
	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
			Detail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := dec.Decode(&msg); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		if msg.Stream != "" {
			_, _ = io.WriteString(log, msg.Stream)
		}
		if e := msg.Error; e != "" || msg.Detail.Message != "" {
			if e == "" {
				e = msg.Detail.Message
			}
			return fmt.Errorf("%s: %s", what, strings.TrimSpace(e))
		}
	}
}

// tarDir writes dir's regular files (not hidden ones) as a tar to w.
func tarDir(dir string, w io.Writer) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "." {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr := &tar.Header{Name: filepath.ToSlash(rel), Mode: int64(info.Mode().Perm()), Size: info.Size(), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
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
