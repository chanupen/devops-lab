package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"sigs.k8s.io/kind/pkg/apis/config/v1alpha4"

	"github.com/chanupen/devops-lab/internal/labs"
)

// Scratch makes the single-node kind clusters scratch labs run on. They
// sit on the kind Podman network beside the main lab and pull through the
// same Zot cache, but share nothing else with it: no host ports, their own
// kubeconfig file.
type Scratch struct {
	home string
	rt   Runtime
	// clusters adapts kind; retain keeps the nodes of a failed create.
	clusters func(log io.Writer, kubeconfig string, retain bool) Clusters
	ready    func(ctx context.Context, kubeconfig string, want int, log io.Writer) error
	// diagnose writes what Podman knows about a node that failed to come up.
	diagnose func(ctx context.Context, node string, w io.Writer)
}

// NewScratch uses the rootless Podman socket and the kind library. home is
// ~/.lab: the containerd mirror files are the main lab's, under home/kind.
func NewScratch(home string) (*Scratch, error) {
	sock, err := PodmanSocket()
	if err != nil {
		return nil, err
	}
	return &Scratch{home: home, rt: NewPodman(sock),
		clusters: func(log io.Writer, kubeconfig string, retain bool) Clusters {
			k := newKindClusters(newKindLogger(log))
			k.kubeconfig = kubeconfig // never ~/.kube/config: the user's current context stays as it is
			k.retain = retain
			return k
		}, ready: waitNodesReady, diagnose: diagnoseNode}, nil
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
	names, err := s.clusters(io.Discard, "", false).List()
	return slices.Contains(names, name), err
}

// Uptime implements labs.Clusters from the node container's state.
func (s *Scratch) Uptime(name string) (labs.Uptime, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := s.rt.Container(ctx, name+"-control-plane")
	if err != nil {
		return labs.Uptime{}, err
	}
	return labs.Uptime{Exists: c.Exists, Running: c.Running, Started: c.StartedAt, Stopped: c.FinishedAt}, nil
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
	kc := s.clusters(log, kubeconfig, false)
	names, err := kc.List()
	if err != nil {
		return err
	}
	if slices.Contains(names, name) {
		logf(log, "kind cluster %q exists", name)
	} else if err := s.create(ctx, name, kubeconfig, log); err != nil {
		return err
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

// create makes the cluster, trying twice. The first try keeps the nodes
// if it fails (kind --retain), so what Podman and the node's systemd said
// goes into the log before they are deleted; the second is a plain create.
// Rootless Podman has failed an exec into a just-started node (exit 126).
func (s *Scratch) create(ctx context.Context, name, kubeconfig string, log io.Writer) error {
	logf(log, "creating kind cluster %q (one node)", name)
	first := s.clusters(log, kubeconfig, true)
	err := first.Create(scratchConfig(name, s.certsDir()), NodeImage)
	if err == nil {
		return nil
	}
	logf(log, "creating %q failed: %v", name, err)
	if s.diagnose != nil {
		s.diagnose(ctx, name+"-control-plane", log)
	}
	logf(log, "removing the failed cluster %q", name)
	if derr := first.Delete(name); derr != nil {
		return fmt.Errorf("%w; removing the failed cluster: %w", err, derr)
	}
	logf(log, "trying once more")
	return s.clusters(log, kubeconfig, false).Create(scratchConfig(name, s.certsDir()), NodeImage)
}

// diagnoseNode writes podman inspect, podman logs and the last two minutes
// of the user journal that mention the node.
func diagnoseNode(ctx context.Context, node string, w io.Writer) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	section := func(title string, out []byte, err error) {
		logf(w, "--- %s ---", title)
		_, _ = w.Write(out)
		if err != nil {
			logf(w, "(%v)", err)
		}
	}
	out, err := exec.CommandContext(ctx, "podman", "inspect", node).CombinedOutput()
	section("podman inspect "+node, out, err)
	out, err = exec.CommandContext(ctx, "podman", "logs", node).CombinedOutput()
	section("podman logs "+node, out, err)
	out, err = exec.CommandContext(ctx, "journalctl", "--user", "--since", "-2min", "--no-pager", "-o", "short-iso").CombinedOutput()
	var keep []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, node) {
			keep = append(keep, l)
		}
	}
	section("journalctl --user --since -2min (lines naming "+node+")", []byte(strings.Join(keep, "\n")+"\n"), err)
	logf(w, "--- end of diagnostics ---")
}

// Delete implements labs.Clusters. A cluster already gone is not an error.
func (s *Scratch) Delete(_ context.Context, name, kubeconfig string, log io.Writer) error {
	if name == ClusterName {
		return fmt.Errorf("%q is the main lab's cluster", name)
	}
	kc := s.clusters(log, kubeconfig, false)
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
