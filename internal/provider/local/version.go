package local

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// nodeVersionFile is where a kind node image records its Kubernetes
// version ("v1.37.0"). The node's image name cannot be trusted for it:
// kind's Podman provider pulls by digest, so the tag is gone.
const nodeVersionFile = "/kind/version"

var nodeVersion = regexp.MustCompile(`^v1\.[0-9]+\.[0-9]+`)

// Errors from ClusterVersion that are a state, not a failure to read.
var (
	ErrNoCluster = errors.New("no such cluster")
	ErrStopped   = errors.New("its node is stopped")
)

// ClusterVersion reads the Kubernetes version ("v1.37.0") of kind cluster
// name from its control-plane node. The node must run: a stopped container
// cannot be exec'd (ErrStopped). ErrNoCluster means there is no node.
func ClusterVersion(ctx context.Context, rt Runtime, name string) (string, error) {
	node := name + "-control-plane"
	c, err := rt.Container(ctx, node)
	switch {
	case err != nil:
		return "", err
	case !c.Exists:
		return "", ErrNoCluster
	case !c.Running:
		return "", ErrStopped
	}
	out, err := rt.Exec(ctx, node, []string{"cat", nodeVersionFile})
	if err != nil {
		return "", fmt.Errorf("read %s in %s: %w", nodeVersionFile, node, err)
	}
	v := strings.TrimSpace(out)
	if !nodeVersion.MatchString(v) {
		return "", fmt.Errorf("%s in %s says %q, not a version such as v1.36.4", nodeVersionFile, node, v)
	}
	return v, nil
}

// VersionMinor is "1.37" from "v1.37.0".
func VersionMinor(v string) string {
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}

// checkClusterVersion refuses a cluster whose version differs from pin or
// cannot be read. A cluster that is not there passes: it will be made at
// the pin. remake is the command that makes the cluster again.
func checkClusterVersion(ctx context.Context, rt Runtime, name, pin, remake string) error {
	v, err := ClusterVersion(ctx, rt, name)
	switch {
	case errors.Is(err, ErrNoCluster):
		return nil
	case errors.Is(err, ErrStopped):
		return fmt.Errorf("kind cluster %s is stopped, so its Kubernetes version cannot be read; start it (podman start %s-control-plane) and try again",
			name, name)
	case err != nil:
		return fmt.Errorf("cannot read the Kubernetes version of kind cluster %s (%w); lab does not run against a cluster of unknown version: %s",
			name, err, remake)
	case VersionMinor(v) != pin:
		return fmt.Errorf("kind cluster %s runs Kubernetes %s, but lab.yaml pins %s; a kind cluster cannot change version, so make it again: %s",
			name, v, pin, remake)
	}
	return nil
}
