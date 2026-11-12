package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr []string // substrings; empty means valid
		check   func(t *testing.T, c Config)
	}{
		{name: "empty file uses defaults", yaml: "", check: func(t *testing.T, c Config) {
			if !reflect.DeepEqual(c, Default()) {
				t.Errorf("got %+v, want defaults", c)
			}
		}},
		{name: "repo lab.yaml shape", yaml: `
mode: local
profile: full
gitops: flux
k8sVersion: "1.37"
cloud:
  region: ap-south-1
  ttl: 2h30m
  owner: someone
`, check: func(t *testing.T, c Config) {
			if c.Profile != "full" || c.GitOps != "flux" || time.Duration(c.Cloud.TTL) != 150*time.Minute || c.Cloud.Owner != "someone" {
				t.Errorf("got %+v", c)
			}
		}},
		{name: "default is minimal plus obs", yaml: "mode: local\n", check: func(t *testing.T, c Config) {
			if c.Profile != "minimal" || !slices.Equal(c.ExtraStacks, []string{"obs"}) {
				t.Errorf("profile %q extra %v, want minimal [obs]", c.Profile, c.ExtraStacks)
			}
		}},
		{name: "extraStacks replaces the default", yaml: "extraStacks: [policy]\n", check: func(t *testing.T, c Config) {
			if !slices.Equal(c.ExtraStacks, []string{"policy"}) {
				t.Errorf("extra = %v, want [policy]", c.ExtraStacks)
			}
		}},
		{name: "empty extraStacks clears the default", yaml: "extraStacks: []\n", check: func(t *testing.T, c Config) {
			if len(c.ExtraStacks) != 0 {
				t.Errorf("extra = %v, want none", c.ExtraStacks)
			}
		}},
		{name: "zot host override", yaml: "zot:\n  addHosts:\n    pkg-containers.githubusercontent.com: 185.199.111.133\n",
			check: func(t *testing.T, c Config) {
				if c.Zot.AddHosts["pkg-containers.githubusercontent.com"] != "185.199.111.133" {
					t.Errorf("zot.addHosts = %v", c.Zot.AddHosts)
				}
			}},
		{name: "zot host override needs a name and an IP", yaml: "zot:\n  addHosts:\n    \"not a name\": 1.2.3.4\n    ghcr.io: fast\n",
			wantErr: []string{`zot.addHosts: "not a name" is not a host name`, `zot.addHosts.ghcr.io: "fast" is not an IP address`}},
		{name: "blank extra stack", yaml: "extraStacks: [\"\"]\n", wantErr: []string{"extraStacks: empty stack name"}},
		{name: "k8sVersion defaults when omitted", yaml: "mode: local\n", check: func(t *testing.T, c Config) {
			if c.K8sVersion != "1.36" {
				t.Errorf("k8sVersion = %q", c.K8sVersion)
			}
		}},
		{name: "k8sVersion unquoted float", yaml: "k8sVersion: 1.37\n", check: func(t *testing.T, c Config) {
			if c.K8sVersion != "1.37" {
				t.Errorf("k8sVersion = %q", c.K8sVersion)
			}
		}},
		// Any minor version parses; lab up checks it against the compatibility table.
		{name: "k8sVersion another minor", yaml: `k8sVersion: "1.35"`, check: func(t *testing.T, c Config) {
			if c.K8sVersion != "1.35" {
				t.Errorf("k8sVersion = %q", c.K8sVersion)
			}
		}},
		{name: "k8sVersion not a minor", yaml: `k8sVersion: "latest"`, wantErr: []string{`k8sVersion "latest"`, `want a minor version such as "1.36"`}},
		{name: "bad mode", yaml: "mode: hybrid", wantErr: []string{`mode "hybrid"`}},
		{name: "bad profile", yaml: "profile: huge", wantErr: []string{`profile "huge"`}},
		{name: "bad gitops", yaml: "gitops: spinnaker", wantErr: []string{`gitops "spinnaker"`}},
		{name: "all errors at once", yaml: "mode: x\nprofile: y\ngitops: z\n", wantErr: []string{`mode "x"`, `profile "y"`, `gitops "z"`}},
		{name: "unknown key", yaml: "profle: full", wantErr: []string{"profle"}},
		{name: "unknown nested key", yaml: "cloud:\n  regoin: us-east-1\n", wantErr: []string{"regoin"}},
		{name: "cloud k8sVersion not a minor", yaml: "cloud:\n  k8sVersion: latest\n", wantErr: []string{`cloud.k8sVersion "latest"`}},
		{name: "wrong region", yaml: "cloud:\n  region: us-east-1\n", wantErr: []string{`cloud.region "us-east-1"`}},
		{name: "bad ttl", yaml: "cloud:\n  ttl: soon\n", wantErr: []string{`ttl "soon"`}},
		{name: "zero ttl", yaml: "cloud:\n  ttl: 0s\n", wantErr: []string{"cloud.ttl: 0s: want between 1m and 8h0m0s"}},
		{name: "ttl over the 8h cap", yaml: "cloud:\n  ttl: 9h\n", wantErr: []string{"cloud.ttl: 9h0m0s: want between 1m and 8h0m0s"}},
		{name: "cloud mode needs owner", yaml: "mode: cloud\n", wantErr: []string{"cloud.owner is required"}},
		{name: "cloud mode with owner", yaml: "mode: cloud\ncloud:\n  owner: someone\n"},
		{name: "not yaml", yaml: "mode: [", wantErr: []string{"yaml"}},
		{name: "repo with deploy key", yaml: "repo:\n  url: git@github.com:o/r.git\n  sshKeyFile: ~/.ssh/k\n",
			check: func(t *testing.T, c Config) {
				if c.Repo.URL != "git@github.com:o/r.git" || c.Repo.SSHKeyFile != "~/.ssh/k" {
					t.Errorf("repo = %+v", c.Repo)
				}
			}},
		{name: "deploy key needs an SSH URL", yaml: "repo:\n  url: https://github.com/o/r.git\n  sshKeyFile: ~/.ssh/k\n",
			wantErr: []string{"needs an SSH URL"}},
		{name: "revision is not configurable", yaml: "repo:\n  url: git@github.com:o/r.git\n  revision: feat/x\n",
			wantErr: []string{"revision"}},
		{name: "unknown repo key", yaml: "repo:\n  branch: main\n", wantErr: []string{"branch"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Parse([]byte(tt.yaml))
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tt.check != nil {
					tt.check(t, c)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got none", tt.wantErr)
			}
			for _, w := range tt.wantErr {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		file    string
		content *string // nil: file absent
		wantErr string
	}{
		{"valid", "lab.yaml", ptr("profile: minimal\n"), ""},
		{"missing", "absent.yaml", nil, "no such file"},
		{"invalid names the file", "bad.yaml", ptr("profile: huge\n"), "bad.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.file)
			if tt.content != nil {
				if err := os.WriteFile(path, []byte(*tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Load(path)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// The checked-in lab.yaml must always load.
func TestRepoLabYAML(t *testing.T) {
	c, err := Load("../../lab.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.K8sVersion != K8sVersion {
		t.Errorf("lab.yaml k8sVersion = %q, want %q", c.K8sVersion, K8sVersion)
	}
}

func ptr(s string) *string { return &s }

func TestIsSSHURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"git@github.com:chanupen/devops-lab.git", true},
		{"ssh://git@github.com/chanupen/devops-lab.git", true},
		{"https://github.com/chanupen/devops-lab.git", false},
		{"https://user@github.com/o/r.git", false},
		{"github.com:o/r.git", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsSSHURL(tt.url); got != tt.want {
			t.Errorf("IsSSHURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestDefaultTTL(t *testing.T) {
	if got := time.Duration(Default().Cloud.TTL); got != 2*time.Hour {
		t.Errorf("default ttl = %s, want 2h", got)
	}
	for d, ok := range map[time.Duration]bool{20 * time.Minute: true, 8 * time.Hour: true, 8*time.Hour + time.Minute: false, 30 * time.Second: false} {
		if (CheckTTL(d) == nil) != ok {
			t.Errorf("CheckTTL(%s) ok = %v, want %v", d, !ok, ok)
		}
	}
}

func TestCheckBuilt(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr error
	}{
		{"default argocd", "", nil},
		{"explicit argocd", "gitops: argocd\n", nil},
		{"flux is known but planned", "gitops: flux\n", ErrFluxPlanned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Parse: %v (flux must stay a known value)", err)
			}
			if err := cfg.CheckBuilt(); !errors.Is(err, tt.wantErr) {
				t.Fatalf("CheckBuilt = %v, want %v", err, tt.wantErr)
			}
		})
	}
	if _, err := Parse([]byte("gitops: weave\n")); err == nil || errors.Is(err, ErrFluxPlanned) {
		t.Errorf("unknown gitops value: err = %v, want a validation error", err)
	}
	if got := ErrFluxPlanned.Error(); got != "gitops: flux is planned, not built; use argocd" {
		t.Errorf("message = %q", got)
	}
}
