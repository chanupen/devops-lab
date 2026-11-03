//go:build podman

package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestPodmanExecLive runs Exec against the host's rootless Podman, in a
// throwaway container of its own (never a lab-* one):
//
//	LAB_TEST_IMAGE=docker.io/library/alpine:3 go test -tags podman -run Live ./internal/provider/local/
//
// The image must have cat and sleep, and be pulled already.
func TestPodmanExecLive(t *testing.T) {
	image := os.Getenv("LAB_TEST_IMAGE")
	if image == "" {
		t.Skip("set LAB_TEST_IMAGE to a local image with cat and sleep")
	}
	sock, err := PodmanSocket()
	if err != nil {
		t.Skip(err)
	}
	name := fmt.Sprintf("devopslab-exectest-%d", os.Getpid())
	if out, err := exec.Command("podman", "run", "-d", "--rm", "--name", name, image, "sleep", "120").CombinedOutput(); err != nil {
		t.Fatalf("podman run: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", "-t", "0", name).Run() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p := NewPodman(sock)
	out, err := p.Exec(ctx, name, []string{"sh", "-c", "printf v1.36.4; echo oops >&2"})
	if err != nil || out != "v1.36.4" {
		t.Errorf("Exec = %q, %v; want v1.36.4", out, err)
	}
	_, err = p.Exec(ctx, name, []string{"cat", "/kind/version"})
	if err == nil || !strings.Contains(err.Error(), "exited 1") || !strings.Contains(err.Error(), "No such file") {
		t.Errorf("missing file: %v", err)
	}
}
