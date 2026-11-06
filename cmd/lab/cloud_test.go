package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ~/.lab/lab.yaml is a symlink into the repo; the repo root is where the
// link points, so the module and bin/reaper.zip are found from anywhere.
func TestRepoRoot(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	cfg := filepath.Join(repo, "lab.yaml")
	if err := os.WriteFile(cfg, []byte("mode: local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "lab.yaml")
	if err := os.Symlink(cfg, link); err != nil {
		t.Fatal(err)
	}
	got, err := repoRoot(link)
	want, _ := filepath.EvalSymlinks(repo)
	if err != nil || got != want {
		t.Errorf("repoRoot = %q, %v; want %q", got, err, want)
	}
}

// The baseline refuses to start without what it needs, before any AWS call.
func TestBaselineChecksInputs(t *testing.T) {
	repo := t.TempDir()
	cfgPath := filepath.Join(repo, "lab.yaml")
	if err := os.WriteFile(cfgPath, []byte("mode: local\ncloud:\n  owner: someone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "state.json")
	run := func() error {
		var out bytes.Buffer
		cmd := newRootCmd()
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs([]string{"cloud", "baseline", "plan", "--config", cfgPath, "--state", statePath})
		return cmd.Execute()
	}
	if err := run(); err == nil || !strings.Contains(err.Error(), "bin/reaper.zip is missing: run make reaper") {
		t.Errorf("without the reaper zip: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "bin", "reaper.zip"), []byte("zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir()) // no tofu
	if err := run(); err == nil || !strings.Contains(err.Error(), "tofu not found") {
		t.Errorf("without tofu: %v", err)
	}
	tofu := filepath.Join(t.TempDir(), "tofu")
	if err := os.WriteFile(tofu, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(tofu))
	if err := run(); err == nil || !strings.Contains(err.Error(), "aws/baseline.tfvars is missing") {
		t.Errorf("without the tfvars: %v", err)
	}
}

func TestBaselineNeedsOwner(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "lab.yaml")
	if err := os.WriteFile(cfgPath, []byte("mode: local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"cloud", "baseline", "plan", "--config", cfgPath, "--state", filepath.Join(t.TempDir(), "s.json")})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "cloud.owner") {
		t.Errorf("err = %v", err)
	}
}

func TestBaselineSummary(t *testing.T) {
	for _, tt := range []struct {
		changes, remote, applying bool
		want                      string
	}{
		{true, false, false, "local (moves into the state bucket after the first apply)\nto apply it: lab cloud baseline apply"},
		{false, true, true, "in the state bucket\nno changes"},
	} {
		var b bytes.Buffer
		if err := writeBaselineSummary(&b, tt.changes, tt.remote, tt.applying); err != nil || !strings.Contains(b.String(), tt.want) {
			t.Errorf("summary = %q, want %q", b.String(), tt.want)
		}
	}
}

// Without a terminal, lab cloud login says it needs one and keeps the
// cached session: it must not drop a working session it cannot replace.
func TestCloudLoginNeedsTerminal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cache := filepath.Join(home, ".aws", "cli", "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(cache, "session.json")
	if err := os.WriteFile(cached, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newRootCmd()
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"cloud", "login"})
	if err := cmd.Execute(); !errors.Is(err, errLoginNeedsTerminal) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(cached); err != nil {
		t.Errorf("cached session removed: %v", err)
	}
}

// The reaper package goes to one fixed path whatever the worktree, with
// the same bytes, so tofu's plan does not change between worktrees.
func TestStageReaperZip(t *testing.T) {
	home := t.TempDir()
	for _, wt := range []string{"wt-a", "wt-b"} {
		src := filepath.Join(t.TempDir(), wt, "bin", "reaper.zip")
		if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(src, []byte("package "+wt), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := stageReaperZip(src, home)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(home, "aws", "reaper.zip"); got != want {
			t.Errorf("%s: staged at %s, want %s", wt, got, want)
		}
		if b, _ := os.ReadFile(got); string(b) != "package "+wt {
			t.Errorf("%s: staged %q", wt, b)
		}
	}
	if _, err := stageReaperZip(filepath.Join(home, "missing.zip"), home); err == nil {
		t.Error("a missing package must fail")
	}
}
