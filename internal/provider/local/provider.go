// Package local is the local-mode provider: kind on rootless Podman, with Zot
// as a pull-through registry cache.
//
// LocalStack is not a stack; Secrets() and ObjectStore() will start it when a
// stack such as secrets needs those APIs.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// ErrUnsupported is returned for stacks this provider cannot run yet.
var ErrUnsupported = errors.New("not supported by the local provider yet")

// Options configure a Provider.
type Options struct {
	Home       string    // ~/.lab; config files go under it
	K8sVersion string    // the config pin; the node image must match it
	Purge      bool      // Destroy("registry") also removes the cache volume
	Log        io.Writer // kind progress lines; nil discards them
}

// Provider runs stacks in local mode. It implements engine.Runner.
type Provider struct {
	rt       Runtime
	clusters Clusters
	opts     Options
	// zotReady waits until Zot answers on the host port.
	zotReady func(ctx context.Context) error
}

// New returns a provider using the rootless Podman socket and the kind library.
func New(opts Options) (*Provider, error) {
	sock, err := PodmanSocket()
	if err != nil {
		return nil, err
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	p := newProvider(NewPodman(sock), newKindClusters(newKindLogger(opts.Log)), opts)
	return p, nil
}

func newProvider(rt Runtime, clusters Clusters, opts Options) *Provider {
	p := &Provider{rt: rt, clusters: clusters, opts: opts}
	p.zotReady = func(ctx context.Context) error {
		return waitHTTP(ctx, fmt.Sprintf("http://127.0.0.1:%d/v2/", ZotPort), 60*time.Second)
	}
	return p
}

// Supports reports whether Apply and Destroy handle the stack.
func (p *Provider) Supports(stack string) bool {
	return slices.Contains([]string{"registry", "cluster"}, stack)
}

// Apply brings one stack up. It is idempotent.
func (p *Provider) Apply(ctx context.Context, stack string) error {
	switch stack {
	case "registry":
		return p.applyRegistry(ctx)
	case "cluster":
		return p.applyCluster(ctx)
	}
	return fmt.Errorf("%s: %w", stack, ErrUnsupported)
}

// Destroy removes one stack. Removing something already gone is not an error.
func (p *Provider) Destroy(ctx context.Context, stack string) error {
	switch stack {
	case "registry":
		return p.destroyRegistry(ctx)
	case "cluster":
		return p.destroyCluster()
	}
	return fmt.Errorf("%s: %w", stack, ErrUnsupported)
}

func (p *Provider) zotDir() string   { return filepath.Join(p.opts.Home, "zot") }
func (p *Provider) certsDir() string { return filepath.Join(p.opts.Home, "kind", "certs.d") }

// applyRegistry writes Zot's config, makes sure the kind network exists, and
// runs Zot on it. A container from an older image or config is replaced; the
// cache volume survives.
func (p *Provider) applyRegistry(ctx context.Context) error {
	cfg, err := zotConfig()
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(p.zotDir(), "config.json"), cfg); err != nil {
		return err
	}
	if err := p.ensureNetwork(ctx); err != nil {
		return err
	}
	hash := configHash(ZotImage, cfg)
	info, err := p.rt.Container(ctx, ZotContainer)
	if err != nil {
		return err
	}
	if info.Exists && info.Labels["lab.config-hash"] != hash {
		if err := p.rt.RemoveContainer(ctx, ZotContainer); err != nil {
			return err
		}
		info = ContainerInfo{}
	}
	if !info.Exists {
		if err := p.rt.PullImage(ctx, ZotImage); err != nil {
			return err
		}
		spec := ContainerSpec{
			Name:    ZotContainer,
			Image:   ZotImage,
			Labels:  map[string]string{"lab": "true", "lab.stack": "registry", "lab.config-hash": hash},
			Ports:   []Port{{HostIP: "127.0.0.1", HostPort: ZotPort, ContainerPort: ZotPort}},
			Volumes: []Volume{{Name: ZotVolume, Dest: "/var/lib/registry"}},
			Binds:   []Bind{{Source: p.zotDir(), Dest: "/etc/zot", ReadOnly: true}},
			Network: KindNetwork,
			Aliases: []string{ZotContainer},
		}
		if err := p.rt.CreateContainer(ctx, spec); err != nil {
			return err
		}
	}
	if !info.Running {
		if err := p.rt.StartContainer(ctx, ZotContainer); err != nil {
			return err
		}
	}
	return p.zotReady(ctx)
}

func (p *Provider) ensureNetwork(ctx context.Context) error {
	ok, err := p.rt.NetworkExists(ctx, KindNetwork)
	if err != nil || ok {
		return err
	}
	return p.rt.CreateNetwork(ctx, NetworkSpec{Name: KindNetwork, IPv6Subnet: kindIPv6Subnet})
}

func (p *Provider) destroyRegistry(ctx context.Context) error {
	if err := p.rt.RemoveContainer(ctx, ZotContainer); err != nil {
		return err
	}
	if p.opts.Purge {
		return p.rt.RemoveVolume(ctx, ZotVolume)
	}
	return nil
}

// applyCluster writes the containerd mirror files and creates the kind
// cluster. An existing cluster with the expected nodes is left alone.
func (p *Provider) applyCluster(ctx context.Context) error {
	minor, err := imageMinor(NodeImage)
	if err != nil {
		return err
	}
	if minor != p.opts.K8sVersion {
		return fmt.Errorf("kind node image %s is Kubernetes %s, but lab.yaml pins %s", NodeImage, minor, p.opts.K8sVersion)
	}
	zot, err := p.rt.Container(ctx, ZotContainer)
	if err != nil {
		return err
	}
	if !zot.Running {
		return errors.New("registry (Zot) is not running; run lab up --stacks registry first")
	}
	for _, u := range upstreams() {
		if err := writeFile(filepath.Join(p.certsDir(), u.Name, "hosts.toml"), []byte(hostsTOML(u))); err != nil {
			return err
		}
	}
	names, err := p.clusters.List()
	if err != nil {
		return err
	}
	if slices.Contains(names, ClusterName) {
		n, err := p.clusters.NodeCount(ClusterName)
		if err != nil {
			return err
		}
		if want := 1 + workers; n != want {
			return fmt.Errorf("kind cluster %q exists with %d nodes, want %d; run lab down first", ClusterName, n, want)
		}
		return nil
	}
	return p.clusters.Create(kindConfig(p.certsDir()), NodeImage)
}

func (p *Provider) destroyCluster() error {
	names, err := p.clusters.List()
	if err != nil {
		return err
	}
	if !slices.Contains(names, ClusterName) {
		return nil
	}
	return p.clusters.Delete(ClusterName)
}

// writeFile writes b to path with private permissions, creating parent dirs.
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// waitHTTP polls url until it returns 200 or the timeout passes.
func waitHTTP(ctx context.Context, url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = fmt.Errorf("HTTP %d", resp.StatusCode)
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s not ready after %s: %w", url, timeout, last)
		case <-time.After(500 * time.Millisecond):
		}
	}
}
