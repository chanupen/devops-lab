package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/state"
)

// fakeProvider supports registry and cluster, like the local provider today.
type fakeProvider struct {
	calls []string
	fail  map[string]bool
}

func (f *fakeProvider) Supports(s string) bool { return s == "registry" || s == "cluster" }

func (f *fakeProvider) Apply(_ context.Context, s string, _ io.Writer) error {
	return f.do("apply " + s)
}

func (f *fakeProvider) Destroy(_ context.Context, s string, _ io.Writer) error {
	return f.do("destroy " + s)
}

func (f *fakeProvider) do(c string) error {
	f.calls = append(f.calls, c)
	if f.fail[c] {
		return errors.New("boom")
	}
	return nil
}

const deployedClusterRegistry = `{"version":1,"mode":"local","stacks":{
	"registry":{"status":"applied"},"cluster":{"status":"applied"}},"ops":[]}`

func TestUpDown(t *testing.T) {
	tests := []struct {
		name       string
		config     string
		state      string
		args       []string
		fail       map[string]bool
		wantCalls  []string
		wantPurge  bool
		wantOut    []string
		wantErr    string
		wantStacks []string // applied stacks afterwards
		wantOp     string   // command of the last op
	}{
		{
			name:      "up cluster and registry",
			args:      []string{"up", "--stacks", "cluster,registry"},
			wantCalls: []string{"apply registry", "apply cluster"},
			wantOut: []string{"$ lab up --stacks cluster,registry", "◐ registry: applying (log: " + "", "● registry: applied in",
				"● cluster: applied in", "done in"},
			wantStacks: []string{"cluster", "registry"},
			wantOp:     "lab up --stacks cluster,registry",
		},
		{
			name:       "up cluster pulls in registry",
			args:       []string{"up", "--stacks", "cluster"},
			wantCalls:  []string{"apply registry", "apply cluster"},
			wantStacks: []string{"cluster", "registry"},
			wantOp:     "lab up --stacks cluster",
		},
		{
			name:       "up when already applied does nothing",
			state:      deployedClusterRegistry,
			args:       []string{"up", "--stacks", "cluster"},
			wantOut:    []string{"= registry", "= cluster", "nothing to do"},
			wantStacks: []string{"cluster", "registry"},
		},
		{
			name:    "up of the whole profile names unsupported stacks before running",
			args:    []string{"up"},
			wantErr: "the local provider cannot run gitops, core, sample-app yet", // the fake routes only registry and cluster
		},
		{
			name:       "failed step is reported and recorded",
			args:       []string{"up", "--stacks", "cluster"},
			fail:       map[string]bool{"apply cluster": true},
			wantCalls:  []string{"apply registry", "apply cluster"},
			wantOut:    []string{"● registry: applied in", "✕ cluster: apply failed after"},
			wantErr:    "apply cluster: boom",
			wantStacks: []string{"registry"},
			wantOp:     "lab up --stacks cluster",
		},
		{
			name:    "stacks and prune are exclusive",
			args:    []string{"up", "--stacks", "cluster", "--prune"},
			wantErr: "none of the others can be",
		},
		{
			name:    "unknown stack",
			args:    []string{"up", "--stacks", "harbor"},
			wantErr: `unknown stack "harbor"`,
		},
		{
			name:       "down needs --yes without a terminal",
			state:      deployedClusterRegistry,
			args:       []string{"down"},
			wantErr:    "Destroy 2 stacks?: pass --yes",
			wantStacks: []string{"cluster", "registry"},
		},
		{
			name:      "down destroys dependents first and keeps the cache",
			state:     deployedClusterRegistry,
			args:      []string{"down", "--yes"},
			wantCalls: []string{"destroy cluster", "destroy registry"},
			wantOut:   []string{"- cluster", "- registry", "● cluster: destroyed in", "● registry: destroyed in"},
			wantOp:    "lab down",
		},
		{
			name:      "down --purge passes purge to the provider",
			state:     deployedClusterRegistry,
			args:      []string{"down", "--purge", "--yes"},
			wantCalls: []string{"destroy cluster", "destroy registry"},
			wantPurge: true,
			wantOp:    "lab down --purge",
		},
		{
			name:    "down with nothing deployed",
			args:    []string{"down", "--yes"},
			wantOut: []string{"nothing deployed"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "lab.yaml")
			statePath := filepath.Join(dir, "state.json")
			cfg := tt.config
			if cfg == "" {
				cfg = "profile: minimal\n"
			}
			if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.state != "" {
				if err := os.WriteFile(statePath, []byte(tt.state), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fp := &fakeProvider{fail: tt.fail}
			var gotPurge, built bool
			factory := func(_ config.Config, home string, purge bool) (provider, error) {
				built, gotPurge = true, purge
				if home != dir {
					t.Errorf("provider home = %s, want the state file's directory %s", home, dir)
				}
				return fp, nil
			}
			cmd := newRootCmdWith(factory)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetIn(strings.NewReader("")) // not a terminal
			cmd.SetArgs(append(tt.args, "--config", cfgPath, "--state", statePath))
			err := cmd.Execute()

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q\n%s", err, tt.wantErr, out.String())
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v\n%s", err, out.String())
			}
			if !slices.Equal(fp.calls, tt.wantCalls) {
				t.Errorf("provider calls = %v, want %v", fp.calls, tt.wantCalls)
			}
			if tt.wantCalls == nil && built && len(fp.calls) > 0 {
				t.Error("provider ran but should not have")
			}
			if gotPurge != tt.wantPurge {
				t.Errorf("purge = %v, want %v", gotPurge, tt.wantPurge)
			}
			for _, w := range tt.wantOut {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, out.String())
				}
			}
			st, err := state.NewStore(statePath).Load()
			if err != nil {
				t.Fatal(err)
			}
			var applied []string
			for name, s := range st.Stacks {
				if s.Status == state.Applied {
					applied = append(applied, name)
				}
			}
			slices.Sort(applied)
			if !slices.Equal(applied, tt.wantStacks) {
				t.Errorf("applied = %v, want %v", applied, tt.wantStacks)
			}
			if tt.wantOp != "" && (len(st.Ops) == 0 || st.Ops[len(st.Ops)-1].Command != tt.wantOp) {
				t.Errorf("ops = %+v, want last command %q", st.Ops, tt.wantOp)
			}
		})
	}
}

func TestDefaultProviderModes(t *testing.T) {
	tests := []struct {
		mode    string
		wantErr string
	}{
		{"cloud", "cloud mode has no provider yet"},
	}
	for _, tt := range tests {
		cfg := config.Default()
		cfg.Mode = tt.mode
		_, err := defaultProvider(cfg, t.TempDir(), false)
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%s: err = %v, want %q", tt.mode, err, tt.wantErr)
		}
	}
}

