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

func TestVersionCmd(t *testing.T) {
	defer func(v string) { version = v }(version)
	tests := []struct{ set, want string }{
		{"dev", "dev\n"},
		{"v0.4.0-12-gabc1234", "v0.4.0-12-gabc1234\n"},
	}
	for _, tt := range tests {
		version = tt.set
		out, err := runLab("version")
		if err != nil || out != tt.want {
			t.Errorf("lab version with %q = %q, %v; want %q", tt.set, out, err, tt.want)
		}
	}
	if _, err := runLab("version", "extra"); err == nil {
		t.Error("lab version took an argument")
	}
}
