package local

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// route answers one method and path (query ignored) with a status and body,
// and records the request body.
type route struct {
	status int
	body   string
}

func fakePodman(t *testing.T, routes map[string]route) (*Podman, *[]string, map[string][]byte) {
	t.Helper()
	var seen []string
	bodies := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + strings.TrimPrefix(r.URL.Path, apiPrefix)
		seen = append(seen, key+"?"+r.URL.RawQuery)
		b, _ := io.ReadAll(r.Body)
		bodies[key] = b
		rt, ok := routes[key]
		if !ok {
			t.Errorf("unexpected request %s", key)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.WriteHeader(rt.status)
		_, _ = io.WriteString(w, rt.body)
	}))
	t.Cleanup(srv.Close)
	return newPodmanHTTP(srv.URL, srv.Client()), &seen, bodies
}

func TestPodmanStatusMapping(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		routes  map[string]route
		call    func(p *Podman) (any, error)
		want    any
		wantErr string
	}{
		{"network exists", map[string]route{"GET /networks/kind/exists": {204, ""}},
			func(p *Podman) (any, error) { return p.NetworkExists(ctx, "kind") }, true, ""},
		{"network missing", map[string]route{"GET /networks/kind/exists": {404, `{"message":"no such network"}`}},
			func(p *Podman) (any, error) { return p.NetworkExists(ctx, "kind") }, false, ""},
		{"container missing", map[string]route{"GET /containers/lab-zot/json": {404, `{"message":"no such container"}`}},
			func(p *Podman) (any, error) { return p.Container(ctx, "lab-zot") }, "missing", ""},
		{"container running", map[string]route{"GET /containers/lab-zot/json": {200,
			`{"ImageName":"img","State":{"Running":true},"Config":{"Labels":{"lab":"true"}}}`}},
			func(p *Podman) (any, error) { return p.Container(ctx, "lab-zot") }, "running img true", ""},
		{"start already running", map[string]route{"POST /containers/lab-zot/start": {304, ""}},
			func(p *Podman) (any, error) { return nil, p.StartContainer(ctx, "lab-zot") }, nil, ""},
		{"remove missing container", map[string]route{"DELETE /containers/lab-zot": {404, `{"message":"no such container"}`}},
			func(p *Podman) (any, error) { return nil, p.RemoveContainer(ctx, "lab-zot") }, nil, ""},
		{"remove missing volume", map[string]route{"DELETE /volumes/lab-zot-data": {404, ""}},
			func(p *Podman) (any, error) { return nil, p.RemoveVolume(ctx, "lab-zot-data") }, nil, ""},
		{"volume in use", map[string]route{"DELETE /volumes/lab-zot-data": {409, `{"message":"volume is being used","response":409}`}},
			func(p *Podman) (any, error) { return nil, p.RemoveVolume(ctx, "lab-zot-data") }, nil, "volume is being used (HTTP 409)"},
		{"create conflict", map[string]route{"POST /containers/create": {500, `{"message":"port 5000 already in use"}`}},
			func(p *Podman) (any, error) { return nil, p.CreateContainer(ctx, ContainerSpec{Name: "x"}) }, nil, "port 5000 already in use"},
		{"error without body", map[string]route{"POST /containers/lab-zot/start": {500, ""}},
			func(p *Podman) (any, error) { return nil, p.StartContainer(ctx, "lab-zot") }, nil, "HTTP 500"},
		{"pull ok", map[string]route{"POST /images/pull": {200, `{"stream":"pulling"}` + "\n" + `{"id":"abc"}`}},
			func(p *Podman) (any, error) { return nil, p.PullImage(ctx, "img") }, nil, ""},
		{"pull error in stream", map[string]route{"POST /images/pull": {200, `{"stream":"pulling"}` + "\n" + `{"error":"manifest unknown"}`}},
			func(p *Podman) (any, error) { return nil, p.PullImage(ctx, "img") }, nil, "manifest unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _, _ := fakePodman(t, tt.routes)
			got, err := tt.call(p)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ci, ok := got.(ContainerInfo); ok {
				got = describe(ci)
			}
			if tt.want != nil && got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPodmanRequests(t *testing.T) {
	ctx := context.Background()
	p, seen, bodies := fakePodman(t, map[string]route{
		"POST /images/pull":          {200, `{"id":"abc"}`},
		"POST /containers/create":    {201, `{"Id":"abc"}`},
		"POST /networks/create":      {200, `{}`},
		"DELETE /containers/lab-zot": {200, `[]`},
	})
	if err := p.PullImage(ctx, ZotImage); err != nil {
		t.Fatal(err)
	}
	if err := p.RemoveContainer(ctx, "lab-zot"); err != nil {
		t.Fatal(err)
	}
	if err := p.CreateNetwork(ctx, NetworkSpec{Name: "kind", IPv6Subnet: kindIPv6Subnet}); err != nil {
		t.Fatal(err)
	}
	spec := ContainerSpec{
		Name: ZotContainer, Image: ZotImage, Labels: map[string]string{"lab": "true"},
		Ports:   []Port{{HostIP: "127.0.0.1", HostPort: 5000, ContainerPort: 5000}},
		Volumes: []Volume{{Name: ZotVolume, Dest: "/var/lib/registry"}},
		Binds:   []Bind{{Source: "/home/u/.lab/zot", Dest: "/etc/zot", ReadOnly: true}},
		Network: KindNetwork, Aliases: []string{ZotContainer},
	}
	if err := p.CreateContainer(ctx, spec); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"POST /images/pull?policy=missing&quiet=true&reference=ghcr.io%2Fproject-zot",
		"DELETE /containers/lab-zot?force=true",
	} {
		found := false
		for _, s := range *seen {
			found = found || strings.HasPrefix(s, want)
		}
		if !found {
			t.Errorf("no request starting %q in %v", want, *seen)
		}
	}

	var net map[string]any
	if err := json.Unmarshal(bodies["POST /networks/create"], &net); err != nil {
		t.Fatal(err)
	}
	if net["name"] != "kind" || net["dns_enabled"] != true || net["ipv6_enabled"] != true {
		t.Errorf("network body = %v", net)
	}

	// Field names follow libpod's SpecGenerator JSON.
	var c struct {
		Name  string `json:"name"`
		Image string `json:"image"`
		Ports []struct {
			HostIP        string `json:"host_ip"`
			HostPort      int    `json:"host_port"`
			ContainerPort int    `json:"container_port"`
		} `json:"portmappings"`
		Volumes []struct{ Name, Dest string } `json:"volumes"`
		Mounts  []struct {
			Destination string   `json:"destination"`
			Type        string   `json:"type"`
			Source      string   `json:"source"`
			Options     []string `json:"options"`
		} `json:"mounts"`
		NetNS struct {
			NSMode string `json:"nsmode"`
		} `json:"netns"`
		Networks map[string]struct {
			Aliases []string `json:"aliases"`
		} `json:"Networks"`
	}
	if err := json.Unmarshal(bodies["POST /containers/create"], &c); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		what string
		ok   bool
	}{
		{"name and image", c.Name == ZotContainer && c.Image == ZotImage},
		{"port", len(c.Ports) == 1 && c.Ports[0].HostIP == "127.0.0.1" && c.Ports[0].HostPort == 5000 && c.Ports[0].ContainerPort == 5000},
		{"volume", len(c.Volumes) == 1 && c.Volumes[0].Name == ZotVolume && c.Volumes[0].Dest == "/var/lib/registry"},
		{"bind relabelled read-only", len(c.Mounts) == 1 && c.Mounts[0].Type == "bind" &&
			strings.Join(c.Mounts[0].Options, ",") == "Z,ro"},
		{"bridge network with alias", c.NetNS.NSMode == "bridge" && len(c.Networks[KindNetwork].Aliases) == 1},
	}
	for _, ch := range checks {
		if !ch.ok {
			t.Errorf("%s: body = %s", ch.what, bodies["POST /containers/create"])
		}
	}
}

// describe makes a ContainerInfo comparable in tables.
func describe(ci ContainerInfo) string {
	switch {
	case !ci.Exists:
		return "missing"
	case !ci.Running:
		return "stopped"
	}
	return "running " + ci.Image + " " + ci.Labels["lab"]
}
