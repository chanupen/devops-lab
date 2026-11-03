package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points HOME at a temp directory, so commands that default to
// ~/.lab (lab doctor, the lab lock) never touch, or wait on, a real lab on
// the machine running the tests, and the Podman socket at one that is not
// there, so no test reads or starts the host's containers. Tests that need
// Podman serve a fake one (fakePodmanSocket).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "lab-test-home-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", home); err != nil {
		panic(err)
	}
	if err := os.Setenv("CONTAINER_HOST", "unix://"+filepath.Join(home, "no-podman.sock")); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
