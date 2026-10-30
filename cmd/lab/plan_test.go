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
	const appliedMinimal = `{"version":1,"mode":"local","stacks":{
		"core":{"status":"applied","updatedAt":"2026-10-30T10:00:00Z"},
		"gitops":{"status":"applied","updatedAt":"2026-10-30T10:00:00Z"}},"ops":[]}`
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
			want: []string{"local mode, standard profile, Kubernetes 1.37", "+ core", "+ sample-app",
				"8 to apply, 0 to destroy, 0 unchanged"},
		},
		{
			name:   "partial state",
			config: "profile: minimal\n",
			state:  appliedMinimal,
			want:   []string{"= core", "= gitops", "+ registry", "2 to apply, 0 to destroy, 2 unchanged"},
		},
		{
			name:   "destroy",
			config: "profile: minimal\n",
			state:  appliedMinimal,
			args:   []string{"--destroy"},
			want:   []string{"- gitops", "- core", "0 to apply, 2 to destroy, 0 unchanged"},
		},
		{
			name:   "destroy with nothing deployed",
			config: "profile: minimal\n",
			args:   []string{"--destroy"},
			want:   []string{"nothing deployed", "0 to apply, 0 to destroy"},
		},
		{
			name:    "invalid config",
			config:  "profile: huge\n",
			wantErr: `profile "huge"`,
		},
		{
			name:    "mode mismatch",
			config:  "mode: cloud\ncloud:\n  owner: someone\n",
			state:   appliedMinimal,
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
			if _, err := os.Stat(statePath); tt.state == "" && err == nil {
				t.Error("plan created a state file; it must not write")
			}
		})
	}
}

func TestPlanCmdJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "lab.yaml")
	if err := os.WriteFile(cfgPath, []byte("profile: minimal\n"), 0o600); err != nil {
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
	if p.K8sVersion != "1.37" || len(p.Steps) != 4 || p.Steps[0].Stack != "core" || p.Steps[0].Action != "apply" {
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
