package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Flags that are refused before any provider, AWS call or lock.
func TestCloudFlagErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfgPath := filepath.Join(dir, "lab.yaml")
	if err := os.WriteFile(cfgPath, []byte("cloud:\n  owner: someone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state.json")
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"up", "--ttl", "20m"}, "--ttl applies to cloud mode only"},
		{[]string{"up", "--mode", "cloud", "--ttl", "9h"}, "--ttl 9h0m0s: want between 1m and 8h0m0s"},
		{[]string{"up", "--mode", "cloud", "--ttl", "10s"}, "want between 1m"},
		{[]string{"tunnel", "--mode", "local"}, "a local lab needs no tunnel"},
		{[]string{"tunnel"}, "the cloud lab has no Gateway yet"},
		{[]string{"extend", "0s"}, "want a positive Go duration"},
	}
	for _, tt := range tests {
		args := append(slices.Clone(tt.args), "--config", cfgPath)
		if tt.args[0] != "tunnel" {
			args = append(args, "--state", state)
		}
		_, err := runLab(args...)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("lab %s: err = %v, want %q", strings.Join(tt.args, " "), err, tt.want)
		}
	}
}
