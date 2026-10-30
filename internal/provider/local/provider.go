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
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/chanupen/devops-lab/internal/kube"
)

// ErrUnsupported is returned for stacks this provider cannot run yet.
var ErrUnsupported = errors.New("not supported by the local provider yet")

// Options configure a Provider.
type Options struct {
	Home       string // ~/.lab; config files go under it
	Kubeconfig string // where the lab cluster's kubeconfig is written
	K8sVersion string // the config pin; the node image must match it
	Purge      bool   // Destroy("registry") also removes the cache volume
}

// Provider runs stacks in local mode. It implements engine.Runner.
type Provider struct {
	rt Runtime
	// clusters returns a kind client that logs to the step's log.
	clusters func(log io.Writer) Clusters
	opts     Options
	// zotReady waits until Zot's /v2/ returns 200.
	zotReady func(ctx context.Context, log io.Writer) error
	// nodesReady waits until the cluster in kubeconfig has want Ready nodes.
	nodesReady func(ctx context.Context, kubeconfig string, want int, log io.Writer) error
}

// New returns a provider using the rootless Podman socket and the kind library.
func New(opts Options) (*Provider, error) {
	sock, err := PodmanSocket()
	if err != nil {
		return nil, err
	}
	clusters := func(log io.Writer) Clusters { return newKindClusters(newKindLogger(log)) }
	return newProvider(NewPodman(sock), clusters, opts), nil
}

func newProvider(rt Runtime, clusters func(io.Writer) Clusters, opts Options) *Provider {
	return &Provider{
		rt: rt, clusters: clusters, opts: opts,
		zotReady: func(ctx context.Context, log io.Writer) error {
			return waitHTTP(ctx, fmt.Sprintf("http://127.0.0.1:%d/v2/", ZotPort), 60*time.Second, log)
		},
		nodesReady: waitNodesReady,
	}
}

// waitNodesReady watches the cluster's nodes for up to three minutes.
func waitNodesReady(ctx context.Context, kubeconfig string, want int, log io.Writer) error {
	cfg, err := kube.RESTConfig(kubeconfig)
	if err != nil {
		return err
	}
	c, err := kube.NewClients(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	return kube.WaitNodesReady(ctx, c.Core, want, log)
}

// Supports reports whether Apply and Destroy handle the stack.
func (p *Provider) Supports(stack string) bool {
	return slices.Contains([]string{"registry", "cluster"}, stack)
}

// Apply brings one stack up. It is idempotent.
func (p *Provider) Apply(ctx context.Context, stack string, log io.Writer) error {
	switch stack {
	case "registry":
		return p.applyRegistry(ctx, log)
	case "cluster":
		return p.applyCluster(ctx, log)
	}
	return fmt.Errorf("%s: %w", stack, ErrUnsupported)
}

// Destroy removes one stack. Removing something already gone is not an error.
func (p *Provider) Destroy(ctx context.Context, stack string, log io.Writer) error {
	switch stack {
	case "registry":
		return p.destroyRegistry(ctx, log)
	case "cluster":
		return p.destroyCluster(log)
	}
	return fmt.Errorf("%s: %w", stack, ErrUnsupported)
}

func (p *Provider) zotDir() string   { return filepath.Join(p.opts.Home, "zot") }
func (p *Provider) certsDir() string { return filepath.Join(p.opts.Home, "kind", "certs.d") }

// applyRegistry writes Zot's config, makes sure the kind network exists, and
// runs Zot on it. A container from an older image or config is replaced; the
// cache volume survives.
func (p *Provider) applyRegistry(ctx context.Context, log io.Writer) error {
	cfg, err := zotConfig()
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(p.zotDir(), "config.json"), cfg); err != nil {
		return err
	}
	if err := p.ensureNetwork(ctx, log); err != nil {
		return err
	}
	hash := configHash(ZotImage, cfg)
	info, err := p.rt.Container(ctx, ZotContainer)
	if err != nil {
		return err
	}
	if info.Exists && info.Labels["lab.config-hash"] != hash {
		logf(log, "%s has an older image or config; replacing it (cache kept)", ZotContainer)
		if err := p.rt.RemoveContainer(ctx, ZotContainer); err != nil {
			return err
		}
		info = ContainerInfo{}
	}
	if !info.Exists {
		logf(log, "pulling %s", ZotImage)
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
		logf(log, "creating container %s", ZotContainer)
		if err := p.rt.CreateContainer(ctx, spec); err != nil {
			return err
		}
	}
	if !info.Running {
		logf(log, "starting %s", ZotContainer)
		if err := p.rt.StartContainer(ctx, ZotContainer); err != nil {
			return err
		}
	}
	return p.zotReady(ctx, log)
}

func (p *Provider) ensureNetwork(ctx context.Context, log io.Writer) error {
	ok, err := p.rt.NetworkExists(ctx, KindNetwork)
	if err != nil || ok {
		return err
	}
	logf(log, "creating Podman network %s", KindNetwork)
	return p.rt.CreateNetwork(ctx, NetworkSpec{Name: KindNetwork, IPv6Subnet: kindIPv6Subnet})
}

func (p *Provider) destroyRegistry(ctx context.Context, log io.Writer) error {
	logf(log, "removing container %s", ZotContainer)
	if err := p.rt.RemoveContainer(ctx, ZotContainer); err != nil {
		return err
	}
	if p.opts.Purge {
		logf(log, "removing volume %s", ZotVolume)
		return p.rt.RemoveVolume(ctx, ZotVolume)
	}
	logf(log, "keeping volume %s (use --purge to remove it)", ZotVolume)
	return nil
}

// applyCluster writes the containerd mirror files, creates the kind cluster
// if it is missing, writes its kubeconfig and waits until every node is Ready.
// An existing cluster with the expected nodes is kept.
func (p *Provider) applyCluster(ctx context.Context, log io.Writer) error {
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
	kc := p.clusters(log)
	names, err := kc.List()
	if err != nil {
		return err
	}
	want := 1 + workers
	if slices.Contains(names, ClusterName) {
		n, err := kc.NodeCount(ClusterName)
		if err != nil {
			return err
		}
		if n != want {
			return fmt.Errorf("kind cluster %q exists with %d nodes, want %d; run lab down first", ClusterName, n, want)
		}
		logf(log, "kind cluster %q exists with %d nodes", ClusterName, n)
	} else if err := kc.Create(kindConfig(p.certsDir()), NodeImage); err != nil {
		return err
	}
	cfg, err := kc.KubeConfig(ClusterName)
	if err != nil {
		return err
	}
	if err := writeFile(p.opts.Kubeconfig, []byte(cfg)); err != nil {
		return err
	}
	logf(log, "wrote kubeconfig %s", p.opts.Kubeconfig)
	return p.nodesReady(ctx, p.opts.Kubeconfig, want, log)
}

func (p *Provider) destroyCluster(log io.Writer) error {
	kc := p.clusters(log)
	names, err := kc.List()
	if err != nil {
		return err
	}
	if slices.Contains(names, ClusterName) {
		if err := kc.Delete(ClusterName); err != nil {
			return err
		}
	} else {
		logf(log, "kind cluster %q is already gone", ClusterName)
	}
	if err := os.Remove(p.opts.Kubeconfig); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func logf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format+"\n", a...) }

// writeFile writes b to path with private permissions, creating parent dirs.
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// waitHTTP polls url until it returns 200 or the timeout passes. Zot runs
// outside the cluster, so there is nothing to watch; polling is the only way.
func waitHTTP(ctx context.Context, url string, timeout time.Duration, log io.Writer) error {
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
				logf(log, "%s returned 200", url)
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
