package local

import (
	"context"
	"io"
	"time"
)

// Runtime is the container engine surface the local provider uses. Podman's
// REST API implements it; tests use a fake.
type Runtime interface {
	NetworkExists(ctx context.Context, name string) (bool, error)
	CreateNetwork(ctx context.Context, n NetworkSpec) error
	PullImage(ctx context.Context, ref string) error
	// Container returns Exists=false, not an error, when there is no such container.
	Container(ctx context.Context, name string) (ContainerInfo, error)
	// Containers lists every container, running or not, whose name starts
	// with prefix, sorted by name.
	Containers(ctx context.Context, prefix string) ([]ContainerInfo, error)
	CreateContainer(ctx context.Context, c ContainerSpec) error
	StartContainer(ctx context.Context, name string) error
	// Exec runs cmd in a running container and returns its stdout. A
	// non-zero exit is an error carrying stderr.
	Exec(ctx context.Context, name string, cmd []string) (string, error)
	// RemoveContainer force-removes the container; a missing one is not an error.
	RemoveContainer(ctx context.Context, name string) error
	// BuildImage builds dir/containerfile as tag, with timestamps set to 0
	// so the same sources make the same image.
	BuildImage(ctx context.Context, dir, containerfile, tag string, log io.Writer) error
	// PushImage pushes a local image to the registry its name says; tls
	// false allows a plain-HTTP registry (the lab's Zot on 127.0.0.1).
	PushImage(ctx context.Context, name string, tls bool, log io.Writer) error
	// RemoveVolume removes a named volume; a missing one is not an error.
	RemoveVolume(ctx context.Context, name string) error
}

// NetworkSpec describes a bridge network with DNS.
type NetworkSpec struct {
	Name       string
	IPv6Subnet string // also enables IPv6; Podman adds an IPv4 subnet itself
}

// ContainerSpec describes a long-running container.
type ContainerSpec struct {
	Name    string
	Image   string
	Labels  map[string]string
	Ports   []Port
	Volumes []Volume // named volumes, created on first use
	Binds   []Bind
	Network string
	Aliases []string
}

// Port publishes a container port on a host address.
type Port struct {
	HostIP        string
	HostPort      uint16
	ContainerPort uint16
}

// Volume mounts a named volume.
type Volume struct {
	Name string
	Dest string
}

// Bind mounts a host directory. SELinux relabels it for this container only.
type Bind struct {
	Source   string
	Dest     string
	ReadOnly bool
}

// ContainerInfo is the part of a container's state the provider checks.
type ContainerInfo struct {
	Name    string
	Exists  bool
	Running bool
	Image   string
	Labels  map[string]string
	// StartedAt is when it last started; FinishedAt when it last stopped
	// (zero if it never has). Lab time is counted from them.
	StartedAt  time.Time
	FinishedAt time.Time
	// HostPorts are the published ports, "5000/tcp" -> ["127.0.0.1:5000"].
	HostPorts map[string][]string
}
