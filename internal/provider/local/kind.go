package local

import (
	"fmt"
	"strings"
	"time"

	"sigs.k8s.io/kind/pkg/apis/config/defaults"
	"sigs.k8s.io/kind/pkg/apis/config/v1alpha4"
	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/log"

	"github.com/chanupen/devops-lab/internal/compat"
)

const (
	// ClusterName gives the kubectl context kind-lab.
	ClusterName = "lab"
	// KindNetwork is the Podman network kind uses. The provider creates it
	// first, as kind would, so Zot can join it before the nodes exist.
	KindNetwork = "kind"
	// kindIPv6Subnet is the subnet kind derives for a network named "kind".
	kindIPv6Subnet = "fc00:f853:ccd:e793::/64"
	workers        = 2
	certsDirInNode = "/etc/containerd/certs.d"
)

// Gateway ports. The control plane publishes these NodePorts on 127.0.0.1 of
// the host; the core stack's Envoy Gateway service must use the same NodePorts.
const (
	GatewayHTTPNodePort  = 30080
	GatewayHTTPSNodePort = 30443
	GatewayHTTPHostPort  = 8080
	GatewayHTTPSHostPort = 8443
)

// AppDomain resolves, with every subdomain, to 127.0.0.1 in public DNS, so
// https://<app>.localtest.me:8443 works on this host and through the tunnel.
const AppDomain = "localtest.me"

// TunnelHost is the Mac's ssh config entry that forwards 8080 and 8443 here.
const TunnelHost = "fedora-lab"

// NodeImages are the node images kind v0.33.0 (the library in go.mod)
// pre-builds, by Kubernetes minor version, with the digests from its
// release notes: kind says to use the digest, so the image is the one built
// for this release. lab.yaml k8sVersion picks one; it must also be in the
// compatibility table (internal/compat). Update this with the kind library.
var NodeImages = map[string]string{
	"1.34": "kindest/node:v1.34.11@sha256:44e222ee2132dab25ff87301682f89eb82c7880ea3a1bf543bfe9708fd08d67d",
	"1.35": "kindest/node:v1.35.8@sha256:07b2536e30b803ed61d1677a79df6115f798ce64c80f9e22f6ed45afd09323c0",
	"1.36": "kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed",
	"1.37": defaults.Image,
}

// containerdPatch points containerd at per-registry hosts.toml files.
const containerdPatch = `[plugins."io.containerd.grpc.v1.cri".registry]
  config_path = "` + certsDirInNode + `"`

// Clusters creates and deletes kind clusters. The kind library implements it;
// tests use a fake.
type Clusters interface {
	List() ([]string, error)
	NodeCount(name string) (int, error)
	Create(cfg *v1alpha4.Cluster, image string) error
	Delete(name string) error
	// KubeConfig returns the external kubeconfig for the cluster.
	KubeConfig(name string) (string, error)
}

// kindConfig returns 1 control plane and 2 workers. Every node mounts certsDir
// (the hosts.toml tree) read-only. Nodes run privileged, so SELinux does not
// confine them and the shared mount needs no relabel; kind's relabel option
// would apply a private label that only one node could use.
func kindConfig(certsDir string) *v1alpha4.Cluster {
	mounts := []v1alpha4.Mount{{HostPath: certsDir, ContainerPath: certsDirInNode, Readonly: true}}
	cp := v1alpha4.Node{
		Role:        v1alpha4.ControlPlaneRole,
		ExtraMounts: mounts,
		ExtraPortMappings: []v1alpha4.PortMapping{
			{ContainerPort: GatewayHTTPNodePort, HostPort: GatewayHTTPHostPort, ListenAddress: "127.0.0.1", Protocol: v1alpha4.PortMappingProtocolTCP},
			{ContainerPort: GatewayHTTPSNodePort, HostPort: GatewayHTTPSHostPort, ListenAddress: "127.0.0.1", Protocol: v1alpha4.PortMappingProtocolTCP},
		},
	}
	c := &v1alpha4.Cluster{
		TypeMeta:                v1alpha4.TypeMeta{Kind: "Cluster", APIVersion: "kind.x-k8s.io/v1alpha4"},
		Name:                    ClusterName,
		Nodes:                   []v1alpha4.Node{cp},
		ContainerdConfigPatches: []string{containerdPatch},
	}
	for range workers {
		c.Nodes = append(c.Nodes, v1alpha4.Node{Role: v1alpha4.WorkerRole, ExtraMounts: mounts})
	}
	return c
}

// SupportedVersions are the Kubernetes versions the local lab can run: a
// kind node image exists and every row in comps supports it (the local rows
// of the compatibility table for the lab's active stacks, ForStacks).
// Oldest first.
func SupportedVersions(comps []compat.Component) []string {
	vs := make([]string, 0, len(NodeImages))
	for v := range NodeImages {
		vs = append(vs, v)
	}
	return compat.Supported(vs, comps)
}

// NodeImage is the kind node image for Kubernetes v. It refuses a version
// that a row in comps does not support, saying which component and which
// versions work.
func NodeImage(v string, comps []compat.Component) (string, error) {
	ok := strings.Join(SupportedVersions(comps), ", ")
	img, found := NodeImages[v]
	if !found {
		return "", fmt.Errorf("k8sVersion %s: kind %s has no node image for it (local mode supports %s); change k8sVersion in lab.yaml",
			v, kindVersion, ok)
	}
	if bad := compat.Unsupported(v, comps); len(bad) > 0 {
		return "", fmt.Errorf("k8sVersion %s is outside the compatibility table: %s (local mode supports %s); change k8sVersion in lab.yaml",
			v, strings.Join(bad, ", "), ok)
	}
	return img, nil
}

// kindVersion is the kind library in go.mod; NodeImages are its images.
const kindVersion = "v0.33.0"

// imageMinor returns "1.37" from "kindest/node:v1.37.0@sha256:...".
func imageMinor(image string) (string, error) {
	_, tag, ok := strings.Cut(image, ":v")
	if !ok {
		return "", fmt.Errorf("node image %q has no version tag", image)
	}
	tag, _, _ = strings.Cut(tag, "@")
	parts := strings.Split(tag, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("node image %q: version %q is not major.minor", image, tag)
	}
	return parts[0] + "." + parts[1], nil
}

// kindClusters adapts the kind library. kind's Create takes no context: a
// cancelled lab up waits for the current create to finish.
type kindClusters struct {
	p    *cluster.Provider
	wait time.Duration
	// kubeconfig is the file kind adds the cluster to and removes it from;
	// "" is kind's default ($KUBECONFIG or ~/.kube/config).
	kubeconfig string
	// retain keeps the nodes of a failed create, so they can be inspected
	// (kind create cluster --retain); the caller deletes them after.
	retain bool
}

func newKindClusters(logger log.Logger) *kindClusters {
	return &kindClusters{
		p:    cluster.NewProvider(cluster.ProviderWithPodman(), cluster.ProviderWithLogger(logger)),
		wait: 3 * time.Minute,
	}
}

func (k *kindClusters) List() ([]string, error) { return k.p.List() }

func (k *kindClusters) NodeCount(name string) (int, error) {
	n, err := k.p.ListNodes(name)
	return len(n), err
}

func (k *kindClusters) Create(cfg *v1alpha4.Cluster, image string) error {
	return k.p.Create(cfg.Name,
		cluster.CreateWithV1Alpha4Config(cfg),
		cluster.CreateWithNodeImage(image),
		cluster.CreateWithWaitForReady(k.wait),
		cluster.CreateWithDisplayUsage(false),
		cluster.CreateWithDisplaySalutation(false),
		cluster.CreateWithKubeconfigPath(k.kubeconfig),
		cluster.CreateWithRetain(k.retain),
	)
}

func (k *kindClusters) KubeConfig(name string) (string, error) { return k.p.KubeConfig(name, false) }

// Delete removes the cluster and its context from k.kubeconfig.
func (k *kindClusters) Delete(name string) error { return k.p.Delete(name, k.kubeconfig) }
