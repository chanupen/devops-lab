package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sigs.k8s.io/kind/pkg/apis/config/v1alpha4"
)

// fakeRuntime records calls and holds container, network and volume state.
type fakeRuntime struct {
	calls      []string
	networks   map[string]bool
	containers map[string]ContainerInfo
	volumes    map[string]bool
	created    []ContainerSpec
	failOn     string // a call name such as "PullImage" that fails
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{networks: map[string]bool{}, containers: map[string]ContainerInfo{}, volumes: map[string]bool{}}
}

func (f *fakeRuntime) call(name string) error {
	f.calls = append(f.calls, name)
	if f.failOn == name {
		return errors.New(name + " failed")
	}
	return nil
}

func (f *fakeRuntime) NetworkExists(_ context.Context, n string) (bool, error) {
	return f.networks[n], f.call("NetworkExists")
}

func (f *fakeRuntime) CreateNetwork(_ context.Context, n NetworkSpec) error {
	if err := f.call("CreateNetwork"); err != nil {
		return err
	}
	f.networks[n.Name] = true
	return nil
}

func (f *fakeRuntime) PullImage(context.Context, string) error { return f.call("PullImage") }

func (f *fakeRuntime) Container(_ context.Context, name string) (ContainerInfo, error) {
	return f.containers[name], f.call("Container")
}

func (f *fakeRuntime) CreateContainer(_ context.Context, c ContainerSpec) error {
	if err := f.call("CreateContainer"); err != nil {
		return err
	}
	f.created = append(f.created, c)
	f.containers[c.Name] = ContainerInfo{Exists: true, Image: c.Image, Labels: c.Labels}
	for _, v := range c.Volumes {
		f.volumes[v.Name] = true
	}
	return nil
}

func (f *fakeRuntime) StartContainer(_ context.Context, name string) error {
	if err := f.call("StartContainer"); err != nil {
		return err
	}
	c := f.containers[name]
	c.Running = true
	f.containers[name] = c
	return nil
}

func (f *fakeRuntime) RemoveContainer(_ context.Context, name string) error {
	if err := f.call("RemoveContainer"); err != nil {
		return err
	}
	delete(f.containers, name)
	return nil
}

func (f *fakeRuntime) RemoveVolume(_ context.Context, name string) error {
	if err := f.call("RemoveVolume"); err != nil {
		return err
	}
	delete(f.volumes, name)
	return nil
}

// fakeClusters is an in-memory kind.
type fakeClusters struct {
	nodes   map[string]int
	created []*v1alpha4.Cluster
	images  []string
	deleted []string
	fail    error
	// failFirst fails that many creates before the next one works.
	failFirst int
	retains   []bool // the retain flag of each Scratch clusters call
}

func (f *fakeClusters) List() ([]string, error) {
	var n []string
	for k := range f.nodes {
		n = append(n, k)
	}
	return n, nil
}

func (f *fakeClusters) NodeCount(name string) (int, error) { return f.nodes[name], nil }

func (f *fakeClusters) Create(cfg *v1alpha4.Cluster, image string) error {
	if f.fail != nil {
		return f.fail
	}
	if f.failFirst > 0 {
		f.failFirst--
		return errors.New(`failed to copy kubeadm config to node: command "podman exec ..." failed with error: exit status 126`)
	}
	f.created = append(f.created, cfg)
	f.images = append(f.images, image)
	f.nodes[cfg.Name] = len(cfg.Nodes)
	return nil
}

func (f *fakeClusters) KubeConfig(name string) (string, error) { return "kubeconfig for " + name, nil }

func (f *fakeClusters) Delete(name string) error {
	f.deleted = append(f.deleted, name)
	delete(f.nodes, name)
	return nil
}

func testProvider(t *testing.T, rt *fakeRuntime, kc *fakeClusters, purge bool) *Provider {
	t.Helper()
	home := t.TempDir()
	p := newProvider(rt, func(io.Writer) Clusters { return kc },
		Options{Home: home, Kubeconfig: filepath.Join(home, "kubeconfig"), K8sVersion: "1.37", Purge: purge})
	p.zotReady = func(context.Context, io.Writer) error { return rt.call("zotReady") }
	p.nodesReady = func(context.Context, string, int, io.Writer) error { return rt.call("nodesReady") }
	return p
}

func runningZot(hash string) ContainerInfo {
	return ContainerInfo{Exists: true, Running: true, Image: ZotImage, Labels: map[string]string{"lab.config-hash": hash}}
}

func currentHash(t *testing.T) string {
	t.Helper()
	cfg, err := zotConfig()
	if err != nil {
		t.Fatal(err)
	}
	return configHash(ZotImage, cfg)
}

func TestApplyRegistry(t *testing.T) {
	hash := currentHash(t)
	tests := []struct {
		name      string
		setup     func(rt *fakeRuntime)
		wantCalls []string
		wantErr   string
	}{
		{
			name: "fresh host creates network and container",
			wantCalls: []string{"NetworkExists", "CreateNetwork", "Container", "PullImage",
				"CreateContainer", "StartContainer", "zotReady"},
		},
		{
			name:      "existing kind network is reused",
			setup:     func(rt *fakeRuntime) { rt.networks[KindNetwork] = true },
			wantCalls: []string{"NetworkExists", "Container", "PullImage", "CreateContainer", "StartContainer", "zotReady"},
		},
		{
			name: "running and current is left alone",
			setup: func(rt *fakeRuntime) {
				rt.networks[KindNetwork] = true
				rt.containers[ZotContainer] = runningZot(hash)
			},
			wantCalls: []string{"NetworkExists", "Container", "zotReady"},
		},
		{
			name: "stopped and current is started",
			setup: func(rt *fakeRuntime) {
				rt.networks[KindNetwork] = true
				c := runningZot(hash)
				c.Running = false
				rt.containers[ZotContainer] = c
			},
			wantCalls: []string{"NetworkExists", "Container", "StartContainer", "zotReady"},
		},
		{
			name: "old config is replaced",
			setup: func(rt *fakeRuntime) {
				rt.networks[KindNetwork] = true
				rt.containers[ZotContainer] = runningZot("stale")
			},
			wantCalls: []string{"NetworkExists", "Container", "RemoveContainer", "PullImage",
				"CreateContainer", "StartContainer", "zotReady"},
		},
		{
			name:      "pull failure stops before create",
			setup:     func(rt *fakeRuntime) { rt.networks[KindNetwork] = true; rt.failOn = "PullImage" },
			wantCalls: []string{"NetworkExists", "Container", "PullImage"},
			wantErr:   "PullImage failed",
		},
		{
			name:      "not ready is an error",
			setup:     func(rt *fakeRuntime) { rt.networks[KindNetwork] = true; rt.failOn = "zotReady" },
			wantCalls: []string{"NetworkExists", "Container", "PullImage", "CreateContainer", "StartContainer", "zotReady"},
			wantErr:   "zotReady failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newFakeRuntime()
			if tt.setup != nil {
				tt.setup(rt)
			}
			p := testProvider(t, rt, &fakeClusters{nodes: map[string]int{}}, false)
			var log bytes.Buffer
			err := p.Apply(context.Background(), "registry", &log)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if !slices.Equal(rt.calls, tt.wantCalls) {
				t.Errorf("calls = %v\nwant    %v", rt.calls, tt.wantCalls)
			}
			b, err := os.ReadFile(filepath.Join(p.zotDir(), "config.json"))
			if err != nil || !strings.Contains(string(b), `"onDemand": true`) {
				t.Errorf("zot config not written: %v", err)
			}
		})
	}
}

func TestRegistryContainerSpec(t *testing.T) {
	rt := newFakeRuntime()
	p := testProvider(t, rt, &fakeClusters{nodes: map[string]int{}}, false)
	if err := p.Apply(context.Background(), "registry", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(rt.created) != 1 {
		t.Fatalf("created %d containers", len(rt.created))
	}
	c := rt.created[0]
	checks := []struct {
		what string
		ok   bool
	}{
		{"name", c.Name == ZotContainer},
		{"pinned image", c.Image == ZotImage && strings.Contains(ZotImage, "@sha256:")},
		{"port on loopback only", len(c.Ports) == 1 && c.Ports[0] == Port{HostIP: "127.0.0.1", HostPort: 5000, ContainerPort: 5000}},
		{"cache volume", slices.Equal(c.Volumes, []Volume{{Name: ZotVolume, Dest: "/var/lib/registry"}})},
		{"config bind read-only", len(c.Binds) == 1 && c.Binds[0].Dest == "/etc/zot" && c.Binds[0].ReadOnly},
		{"on kind network with alias", c.Network == KindNetwork && slices.Contains(c.Aliases, ZotContainer)},
		{"lab labels", c.Labels["lab"] == "true" && c.Labels["lab.config-hash"] == currentHash(t)},
	}
	for _, ch := range checks {
		if !ch.ok {
			t.Errorf("%s: spec = %+v", ch.what, c)
		}
	}
}

func TestApplyCluster(t *testing.T) {
	hash := currentHash(t)
	tests := []struct {
		name        string
		zot         ContainerInfo
		nodes       map[string]int
		k8s         string
		kindErr     error
		wantCreated bool
		wantErr     string
	}{
		{name: "creates when missing", zot: runningZot(hash), nodes: map[string]int{}, k8s: "1.37", wantCreated: true},
		{name: "existing complete cluster is kept", zot: runningZot(hash), nodes: map[string]int{ClusterName: 3}, k8s: "1.37"},
		{name: "other kind clusters are ignored", zot: runningZot(hash), nodes: map[string]int{"kind": 1}, k8s: "1.37", wantCreated: true},
		{name: "incomplete cluster is an error", zot: runningZot(hash), nodes: map[string]int{ClusterName: 1}, k8s: "1.37",
			wantErr: "exists with 1 nodes, want 3"},
		{name: "registry must run first", zot: ContainerInfo{}, nodes: map[string]int{}, k8s: "1.37",
			wantErr: "registry (Zot) is not running"},
		{name: "pin mismatch", zot: runningZot(hash), nodes: map[string]int{}, k8s: "1.36",
			wantErr: "lab.yaml pins 1.36"},
		{name: "kind failure surfaces", zot: runningZot(hash), nodes: map[string]int{}, k8s: "1.37",
			kindErr: errors.New("node not ready"), wantErr: "node not ready"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newFakeRuntime()
			rt.containers[ZotContainer] = tt.zot
			kc := &fakeClusters{nodes: tt.nodes, fail: tt.kindErr}
			p := testProvider(t, rt, kc, false)
			p.opts.K8sVersion = tt.k8s
			rt.calls = nil
			err := p.Apply(context.Background(), "cluster", io.Discard)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if got := len(kc.created) == 1; got != tt.wantCreated {
				t.Fatalf("created = %v, want %v", got, tt.wantCreated)
			}
			if tt.wantCreated && kc.images[0] != NodeImage {
				t.Errorf("image = %s, want %s", kc.images[0], NodeImage)
			}
			if tt.wantErr == "" {
				if b, err := os.ReadFile(p.opts.Kubeconfig); err != nil || string(b) != "kubeconfig for lab" {
					t.Errorf("kubeconfig = %q, %v", b, err)
				}
				if !slices.Contains(rt.calls, "nodesReady") {
					t.Errorf("calls = %v, want a wait for Ready nodes", rt.calls)
				}
				for _, u := range upstreams() {
					b, err := os.ReadFile(filepath.Join(p.certsDir(), u.Name, "hosts.toml"))
					if err != nil || !strings.Contains(string(b), "lab-zot:5000/v2/"+u.Name) {
						t.Errorf("hosts.toml for %s: %v %q", u.Name, err, b)
					}
				}
			}
		})
	}
}

func TestDestroy(t *testing.T) {
	tests := []struct {
		name        string
		stack       string
		purge       bool
		nodes       map[string]int
		wantCalls   []string
		wantDeleted []string
		wantVolume  bool
	}{
		{name: "registry keeps the cache", stack: "registry", wantCalls: []string{"RemoveContainer"}, wantVolume: true},
		{name: "registry purge removes the cache", stack: "registry", purge: true,
			wantCalls: []string{"RemoveContainer", "RemoveVolume"}},
		{name: "cluster is deleted", stack: "cluster", nodes: map[string]int{ClusterName: 3, "kind": 1},
			wantDeleted: []string{ClusterName}, wantVolume: true},
		{name: "missing cluster is fine", stack: "cluster", nodes: map[string]int{"kind": 1}, wantVolume: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newFakeRuntime()
			rt.containers[ZotContainer] = runningZot("x")
			rt.volumes[ZotVolume] = true
			if tt.nodes == nil {
				tt.nodes = map[string]int{}
			}
			kc := &fakeClusters{nodes: tt.nodes}
			p := testProvider(t, rt, kc, tt.purge)
			if err := writeFile(p.opts.Kubeconfig, []byte("x")); err != nil {
				t.Fatal(err)
			}
			if err := p.Destroy(context.Background(), tt.stack, io.Discard); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(p.opts.Kubeconfig); (err == nil) != (tt.stack != "cluster") {
				t.Errorf("kubeconfig present = %v after destroying %s", err == nil, tt.stack)
			}
			if !slices.Equal(rt.calls, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", rt.calls, tt.wantCalls)
			}
			if !slices.Equal(kc.deleted, tt.wantDeleted) {
				t.Errorf("deleted = %v, want %v", kc.deleted, tt.wantDeleted)
			}
			if rt.volumes[ZotVolume] != tt.wantVolume {
				t.Errorf("volume present = %v, want %v", rt.volumes[ZotVolume], tt.wantVolume)
			}
		})
	}
}

func TestUnsupportedStacks(t *testing.T) {
	p := testProvider(t, newFakeRuntime(), &fakeClusters{nodes: map[string]int{}}, false)
	tests := []struct {
		stack string
		want  bool
	}{
		{"registry", true}, {"cluster", true}, {"gitops", false}, {"obs", false},
	}
	for _, tt := range tests {
		if got := p.Supports(tt.stack); got != tt.want {
			t.Errorf("Supports(%s) = %v, want %v", tt.stack, got, tt.want)
		}
		if tt.want {
			continue
		}
		for name, f := range map[string]func(context.Context, string, io.Writer) error{"Apply": p.Apply, "Destroy": p.Destroy} {
			if err := f(context.Background(), tt.stack, io.Discard); !errors.Is(err, ErrUnsupported) {
				t.Errorf("%s(%s) = %v, want ErrUnsupported", name, tt.stack, err)
			}
		}
	}
}

func TestKindConfig(t *testing.T) {
	c := kindConfig("/home/u/.lab/kind/certs.d")
	roles := map[v1alpha4.NodeRole]int{}
	for _, n := range c.Nodes {
		roles[n.Role]++
		if len(n.ExtraMounts) != 1 || n.ExtraMounts[0].ContainerPath != "/etc/containerd/certs.d" ||
			!n.ExtraMounts[0].Readonly || n.ExtraMounts[0].SelinuxRelabel {
			t.Errorf("%s mounts = %+v", n.Role, n.ExtraMounts)
		}
	}
	if roles[v1alpha4.ControlPlaneRole] != 1 || roles[v1alpha4.WorkerRole] != 2 {
		t.Errorf("roles = %v, want 1 control plane and 2 workers", roles)
	}
	ports := c.Nodes[0].ExtraPortMappings
	want := []struct{ container, host int32 }{{30080, 8080}, {30443, 8443}}
	if len(ports) != len(want) {
		t.Fatalf("ports = %+v", ports)
	}
	for i, w := range want {
		if ports[i].ContainerPort != w.container || ports[i].HostPort != w.host || ports[i].ListenAddress != "127.0.0.1" {
			t.Errorf("port %d = %+v, want %d->%d on 127.0.0.1", i, ports[i], w.host, w.container)
		}
	}
	if c.Name != ClusterName || len(c.ContainerdConfigPatches) != 1 ||
		!strings.Contains(c.ContainerdConfigPatches[0], `config_path = "/etc/containerd/certs.d"`) {
		t.Errorf("cluster = %+v", c)
	}
}

func TestImageMinor(t *testing.T) {
	tests := []struct {
		image, want, wantErr string
	}{
		{"kindest/node:v1.37.0@sha256:abc", "1.37", ""},
		{"kindest/node:v1.36.4", "1.36", ""},
		{"kindest/node@sha256:abc", "", "no version tag"},
		{"kindest/node:v1", "", "not major.minor"},
	}
	for _, tt := range tests {
		got, err := imageMinor(tt.image)
		if got != tt.want || (tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr))) {
			t.Errorf("imageMinor(%q) = %q, %v; want %q, %q", tt.image, got, err, tt.want, tt.wantErr)
		}
	}
	// The kind library's default image must follow the config pin.
	if m, err := imageMinor(NodeImage); err != nil || m != "1.37" {
		t.Errorf("NodeImage %s is %q, %v; want 1.37", NodeImage, m, err)
	}
}

func TestWaitHTTP(t *testing.T) {
	tests := []struct {
		name     string
		notReady int32 // requests answered 503 before 200s
		timeout  time.Duration
		wantErr  string
	}{
		{"ready at once", 0, 2 * time.Second, ""},
		{"ready after two 503s", 2, 5 * time.Second, ""},
		{"never ready", 1000, 1200 * time.Millisecond, "HTTP 503"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2/" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if n.Add(1) <= tt.notReady {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			var log bytes.Buffer
			err := waitHTTP(context.Background(), srv.URL+"/v2/", tt.timeout, &log)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(log.String(), "/v2/ returned 200") {
					t.Errorf("log = %q", log.String())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestAppAccess(t *testing.T) {
	p := testProvider(t, newFakeRuntime(), &fakeClusters{nodes: map[string]int{}}, false)
	tests := []struct{ host, want string }{
		{"argocd.localtest.me", "https://argocd.localtest.me:8443"},
		{"shop.localtest.me", "https://shop.localtest.me:8443"},
	}
	for _, tt := range tests {
		if got := p.AppURL(tt.host); got != tt.want {
			t.Errorf("AppURL(%s) = %s, want %s", tt.host, got, tt.want)
		}
	}
	hint := p.AccessHint()
	if strings.Contains(hint, "\n") || !strings.Contains(hint, "ssh -N fedora-lab") || !strings.Contains(hint, "8443") {
		t.Errorf("hint = %q, want one line naming ssh -N fedora-lab and the ports", hint)
	}
	if got, want := TunnelHint(), "on the Mac: ssh -N fedora-lab"; got != want {
		t.Errorf("TunnelHint() = %q, want %q", got, want)
	}
}
