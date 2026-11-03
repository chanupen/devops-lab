package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanCmd(t *testing.T) {
	const partial = `{"version":1,"mode":"local","revision":"main","stacks":{
		"cluster":{"status":"applied","updatedAt":"2026-10-30T10:00:00Z"},
		"gitops":{"status":"applied","updatedAt":"2026-10-30T10:00:00Z"}},"ops":[]}`
	// minimal plus chaos and obs left from a full run.
	const withExtras = `{"version":1,"mode":"local","revision":"main","stacks":{
		"cluster":{"status":"applied"},"gitops":{"status":"applied"},"core":{"status":"applied"},
		"registry":{"status":"applied"},"sample-app":{"status":"applied"},
		"obs":{"status":"applied"},"chaos":{"status":"applied"}},"ops":[]}`
	tests := []struct {
		name    string
		config  string
		state   string // empty: no state file
		args    []string
		want    []string // substrings of stdout
		wantErr string
	}{
		{
			name:   "fresh standard",
			config: "profile: standard\n",
			// lab plan checks what the local provider supports: the rest is
			// skipped at plan time rather than failing lab up later.
			want: []string{"local mode, standard profile, Kubernetes 1.36", "+ cluster", "+ core", "+ sample-app", "+ obs",
				"- secrets     skip: not supported by the local provider yet",
				"+ policy      not deployed",
				"7 to apply, 0 to destroy, 0 unchanged, 2 skipped",
				"warning: 2 stacks skipped, not supported by the local provider yet: secrets, scaling"},
		},
		{
			name:   "default profile is minimal plus obs, all supported",
			config: "mode: local\n",
			want:   []string{"local mode, minimal + obs profile", "+ obs", "6 to apply, 0 to destroy, 0 unchanged"},
		},
		{
			name:   "partial state",
			config: "profile: minimal\nextraStacks: []\n",
			state:  partial,
			want:   []string{"= cluster", "= gitops", "+ core", "+ registry", "3 to apply, 0 to destroy, 2 unchanged"},
		},
		{
			name:   "extras kept with a warning",
			config: "profile: minimal\nextraStacks: []\n",
			state:  withExtras,
			want: []string{"= obs", "not in profile; run lab up --prune to remove", "0 to apply, 0 to destroy, 7 unchanged",
				"warning: 2 deployed stacks are not in profile minimal, kept: obs, chaos. Run lab up --prune to remove."},
		},
		{
			name:   "prune destroys extras",
			config: "profile: minimal\nextraStacks: []\n",
			state:  withExtras,
			args:   []string{"--prune"},
			want:   []string{"- chaos", "- obs", "0 to apply, 2 to destroy, 5 unchanged"},
		},
		{
			name:    "prune and destroy are exclusive",
			config:  "profile: minimal\nextraStacks: []\n",
			args:    []string{"--prune", "--destroy"},
			wantErr: "none of the others can be",
		},
		{
			name:   "destroy",
			config: "profile: minimal\nextraStacks: []\n",
			state:  partial,
			args:   []string{"--destroy"},
			want:   []string{"- gitops", "- cluster", "0 to apply, 2 to destroy, 0 unchanged"},
		},
		{
			name:   "destroy with nothing deployed",
			config: "profile: minimal\nextraStacks: []\n",
			args:   []string{"--destroy"},
			want:   []string{"nothing deployed", "0 to apply, 0 to destroy"},
		},
		{
			name:    "invalid config",
			config:  "profile: huge\n",
			wantErr: `profile "huge"`,
		},
		{
			// Cloud mode plans the same stacks as local; until the cloud
			// GitOps overlay exists, the GitOps stacks are skipped.
			name:   "--mode cloud plans the profile with the cloud provider",
			config: "cloud:\n  owner: someone\n",
			args:   []string{"--mode", "cloud"},
			want: []string{"cloud mode, minimal + obs profile, Kubernetes chosen by lab (EKS standard support)", "+ registry", "+ cluster",
				"- gitops      skip: not supported by the cloud provider yet",
				"- sample-app  skip: not supported by the cloud provider yet",
				"2 to apply, 0 to destroy, 0 unchanged, 4 skipped",
				"warning: 4 stacks skipped, not supported by the cloud provider yet: gitops, core, sample-app, obs"},
		},
		{
			name:    "--mode cloud needs an owner",
			config:  "mode: local\n",
			args:    []string{"--mode", "cloud"},
			wantErr: "cloud.owner is required",
		},
		{
			name:    "--mode must be known",
			config:  "mode: local\n",
			args:    []string{"--mode", "moon"},
			wantErr: `mode "moon"`,
		},
		{
			name:    "mode mismatch",
			config:  "mode: cloud\ncloud:\n  owner: someone\n",
			state:   partial,
			wantErr: "run lab down in local mode first",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "lab.yaml")
			statePath := filepath.Join(dir, "state.json")
			if err := os.WriteFile(cfgPath, []byte(tt.config), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.state != "" {
				if err := os.WriteFile(statePath, []byte(tt.state), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			out, err := runLab(append([]string{"plan", "--config", cfgPath, "--state", statePath}, tt.args...)...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			if !strings.Contains(strings.Join(tt.want, " "), "warning:") && strings.Contains(out, "warning:") {
				t.Errorf("unexpected warning:\n%s", out)
			}
			if _, err := os.Stat(statePath); tt.state == "" && err == nil {
				t.Error("plan created a state file; it must not write")
			}
		})
	}
}

func TestPlanCmdJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "lab.yaml")
	if err := os.WriteFile(cfgPath, []byte("profile: minimal\nextraStacks: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runLab("plan", "--json", "--config", cfgPath, "--state", filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		K8sVersion string `json:"k8sVersion"`
		Steps      []struct{ Stack, Action string }
	}
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if p.K8sVersion != "1.36" || len(p.Steps) != 5 || p.Steps[0].Stack != "registry" || p.Steps[0].Action != "apply" {
		t.Errorf("unexpected plan: %+v", p)
	}
}

func runLab(args ...string) (string, error) {
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// Each mode keeps its own state, so a cloud lab can run beside a local one.
func TestModeStatePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "lab.yaml")
	if err := os.WriteFile(cfgPath, []byte("cloud:\n  owner: someone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for mode, want := range map[string]string{
		"local": filepath.Join(home, ".lab", "state.json"),
		"cloud": filepath.Join(home, ".lab", "cloud", "state.json"),
	} {
		f := labFlags{cfgPath: cfgPath, mode: mode}
		_, store, _, err := f.load()
		if err != nil {
			t.Fatal(err)
		}
		if store.Path() != want {
			t.Errorf("%s state = %s, want %s", mode, store.Path(), want)
		}
	}
}
