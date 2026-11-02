package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	labaws "github.com/chanupen/devops-lab/internal/provider/aws"
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

// Stale tagging-index entries are listed apart and are not leftovers.
func TestWriteLeftovers(t *testing.T) {
	stale := labaws.Tagged{ARN: "arn:aws:ec2:ap-south-1:111122223333:instance/i-05f7266bbe4e3b48e",
		Tags: map[string]string{"expires-at": "2026-11-02T11:03:00Z"}}
	vpc := labaws.Tagged{ARN: "arn:aws:ec2:ap-south-1:111122223333:vpc/vpc-1", Tags: map[string]string{"expires-at": "2026-11-02T11:03:00Z"}}
	tests := []struct {
		name   string
		rep    labaws.LeftoverReport
		has    []string
		hasNot []string
	}{
		{"only stale entries", labaws.LeftoverReport{Stale: []labaws.Tagged{stale}},
			[]string{"nothing tagged lab=true is left outside the baseline", "stale index entries (ignored) (1)", "ec2:instance/i-05f7266bbe4e3b48e"},
			[]string{"may still bill"}},
		{"a real leftover", labaws.LeftoverReport{Left: []labaws.Tagged{vpc}, Stale: []labaws.Tagged{stale}},
			[]string{"outside the baseline (1); these may still bill", "ec2:vpc/vpc-1  expires-at=2026-11-02T11:03:00Z", "stale index entries (ignored) (1)"}, nil},
		{"a failed lookup", labaws.LeftoverReport{Left: []labaws.Tagged{vpc}, Errors: []string{"check vpc-1: Throttling"}},
			[]string{"could not check (listed above as still there)", "Throttling"}, []string{"stale index"}},
	}
	for _, tt := range tests {
		var b strings.Builder
		if err := writeLeftovers(&b, tt.rep); err != nil {
			t.Fatal(err)
		}
		for _, h := range tt.has {
			if !strings.Contains(b.String(), h) {
				t.Errorf("%s: lacks %q:\n%s", tt.name, h, b.String())
			}
		}
		for _, h := range tt.hasNot {
			if strings.Contains(b.String(), h) {
				t.Errorf("%s: has %q:\n%s", tt.name, h, b.String())
			}
		}
	}
}
