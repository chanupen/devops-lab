package main

import (
	"os"
	"testing"
)

// TestMain points HOME at a temp directory, so commands that default to
// ~/.lab (lab doctor, the lab lock) never touch, or wait on, a real lab on
// the machine running the tests.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "lab-test-home-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", home); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
