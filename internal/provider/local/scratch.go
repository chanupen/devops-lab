package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"sigs.k8s.io/kind/pkg/apis/config/v1alpha4"
)

// Scratch makes the single-node kind clusters scratch labs run on. They
// sit on the kind Podman network beside the main lab and pull through the
// same Zot cache, but share nothing else with it: no host ports, their own
// kubeconfig file.
type Scratch struct {
	home     string
	rt       Runtime
	clusters func(log io.Writer, kubeconfig string) Clusters
	ready    func(ctx context.Context, kubeconfig string, want int, log io.Writer) error
}

// NewScratch uses the rootless Podman socket and the kind library. home is
// ~/.lab: the containerd mirror files are the main lab's, under home/kind.
func NewScratch(home string) (*Scratch, error) {
	sock, err := PodmanSocket()
	if err != nil {
		return nil, err
	}
	return &Scratch{home: home, rt: NewPodman(sock),
		clusters: func(log io.Writer, kubeconfig string) Clusters {
			k := newKindClusters(newKindLogger(log))
			k.kubeconfig = kubeconfig // never ~/.kube/config: the user's current context stays as it is
			return k
		}, ready: waitNodesReady}, nil
}

func (s *Scratch) certsDir() string { return filepath.Join(s.home, "kind", "certs.d") }

// scratchConfig is one control-plane node that takes workloads, with the
// mirror files mounted like the main lab's nodes and no port mappings.
func scratchConfig(name, certsDir string) *v1alpha4.Cluster {
	return &v1alpha4.Cluster{
		TypeMeta: v1alpha4.TypeMeta{Kind: "Cluster", APIVersion: "kind.x-k8s.io/v1alpha4"},
		Name:     name,
		Nodes: []v1alpha4.Node{{
			Role:        v1alpha4.ControlPlaneRole,
			ExtraMounts: []v1alpha4.Mount{{HostPath: certsDir, ContainerPath: certsDirInNode, Readonly: true}},
		}},
		ContainerdConfigPatches: []string{containerdPatch},
	}
}

// Exists implements labs.Clusters.
func (s *Scratch) Exists(name string) (bool, error) {
	names, err := s.clusters(io.Discard, "").List()
	return slices.Contains(names, name), err
}

// Create implements labs.Clusters. Without a running Zot, containerd's
// mirror files fall back to the upstream registries, so the cluster still
// works, only slower; it says so.
func (s *Scratch) Create(ctx context.Context, name, kubeconfig string, log io.Writer) error {
	if name == ClusterName {
		return fmt.Errorf("%q is the main lab's cluster", name)
	}
	if zot, err := s.rt.Container(ctx, ZotContainer); err != nil || !zot.Running {
		logf(log, "image cache %s is not running: images come straight from the registries (lab up --stacks registry starts it)", ZotContainer)
	} else {
		logf(log, "image cache: %s (shared with the main lab)", ZotContainer)
	}
	for _, u := range upstreams() {
		if err := writeFile(filepath.Join(s.certsDir(), u.Name, "hosts.toml"), []byte(hostsTOML(u))); err != nil {
			return err
		}
	}
	kc := s.clusters(log, kubeconfig)
	names, err := kc.List()
	if err != nil {
		return err
	}
	if slices.Contains(names, name) {
		logf(log, "kind cluster %q exists", name)
	} else {
		logf(log, "creating kind cluster %q (one node)", name)
		if err := kc.Create(scratchConfig(name, s.certsDir()), NodeImage); err != nil {
			// Rootless Podman sometimes fails an exec into a node that has
			// just started; kind removes the node, so one more try is clean.
			logf(log, "creating %q failed (%v); trying once more", name, err)
			if err := kc.Create(scratchConfig(name, s.certsDir()), NodeImage); err != nil {
				return err
			}
		}
	}
	cfg, err := kc.KubeConfig(name)
	if err != nil {
		return err
	}
	// kind wrote it already; write it again so it holds this cluster alone.
	if err := writeFile(kubeconfig, []byte(cfg)); err != nil {
		return err
	}
	logf(log, "wrote kubeconfig %s", kubeconfig)
	return s.ready(ctx, kubeconfig, 1, log)
}

// Delete implements labs.Clusters. A cluster already gone is not an error.
func (s *Scratch) Delete(_ context.Context, name, kubeconfig string, log io.Writer) error {
	if name == ClusterName {
		return fmt.Errorf("%q is the main lab's cluster", name)
	}
	kc := s.clusters(log, kubeconfig)
	names, err := kc.List()
	if err != nil {
		return err
	}
	if slices.Contains(names, name) {
		logf(log, "deleting kind cluster %q", name)
		if err := kc.Delete(name); err != nil {
			return err
		}
	} else {
		logf(log, "kind cluster %q is already gone", name)
	}
	if err := os.Remove(kubeconfig); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
