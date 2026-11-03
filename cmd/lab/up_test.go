package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/provider/local"
	"github.com/chanupen/devops-lab/internal/state"
)

// fakeProvider supports registry and cluster, and lists app URLs.
type fakeProvider struct {
	also    []string // stacks supported beyond registry and cluster
	calls   []string
	fail    map[string]bool
	urls    []string
	urlsErr error
	stopped []string // lab containers a reboot stopped
	// versionErr is what CheckK8sVersion says about the pin.
	versionErr error
	// clusterErr is what CheckClusterVersion says about the running cluster.
	clusterErr error
}

// StartStopped implements stoppedStarter: it starts f.stopped, recording each.
func (f *fakeProvider) StartStopped(_ context.Context, log io.Writer) ([]local.Unit, error) {
	for _, c := range f.stopped {
		_, _ = fmt.Fprintf(log, "%s was stopped; starting it\n", c)
		if err := f.do("start " + c); err != nil {
			return nil, err
		}
	}
	f.stopped = nil
	return nil, nil
}

// CheckK8sVersion implements versionGate.
func (f *fakeProvider) CheckK8sVersion(context.Context) error { return f.versionErr }

// CheckClusterVersion implements clusterVersionGate; it records the call,
// so a test sees it ran after the stopped containers started.
func (f *fakeProvider) CheckClusterVersion(context.Context) error {
	if f.clusterErr == nil {
		return nil
	}
	f.calls = append(f.calls, "check cluster version")
	return f.clusterErr
}

func (f *fakeProvider) AppURLs(context.Context) ([]string, error) { return f.urls, f.urlsErr }

func (f *fakeProvider) AccessHint() string { return "On the Mac, run ssh -N fedora-lab first." }

func (f *fakeProvider) Supports(s string) bool {
	return s == "registry" || s == "cluster" || slices.Contains(f.also, s)
}

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
		stopped    []string // containers stopped before the run
		versionErr error    // the provider refuses the k8sVersion pin
		clusterErr error    // the provider refuses the running cluster's version
	}{
		{
			name:       "up starts the stopped cluster, then refuses it on another version",
			state:      deployedClusterRegistry,
			args:       []string{"up"},
			stopped:    []string{"lab-control-plane"},
			clusterErr: errors.New("kind cluster lab runs Kubernetes v1.37.0, but lab.yaml pins 1.36"),
			wantCalls:  []string{"start lab-control-plane", "check cluster version"},
			wantErr:    "runs Kubernetes v1.37.0, but lab.yaml pins 1.36",
			wantStacks: []string{"cluster", "registry"},
		},
		{
			name:       "up refuses a cluster of unknown version",
			state:      deployedClusterRegistry,
			args:       []string{"up"},
			clusterErr: errors.New("cannot read the Kubernetes version of kind cluster lab"),
			wantCalls:  []string{"check cluster version"},
			wantErr:    "cannot read the Kubernetes version",
			wantStacks: []string{"cluster", "registry"},
		},
		{
			name:       "up refuses a pin outside the compatibility table before doing anything",
			args:       []string{"up", "--stacks", "cluster"},
			versionErr: errors.New("k8sVersion 1.37 is outside the compatibility table: Argo CD v3.5.5 (1.33–1.36)"),
			wantErr:    "k8sVersion 1.37 is outside the compatibility table",
		},
		{
			name:       "a refused pin starts no stopped containers",
			state:      deployedClusterRegistry,
			args:       []string{"up"},
			stopped:    []string{"lab-control-plane", "lab-zot"},
			versionErr: errors.New("k8sVersion 1.37 is outside the compatibility table"),
			wantErr:    "outside the compatibility table",
			wantStacks: []string{"cluster", "registry"},
		},
		{
			name:       "up refuses a cluster on another version",
			state:      deployedClusterRegistry,
			args:       []string{"up"},
			versionErr: errors.New("the main cluster runs Kubernetes 1.37, but lab.yaml pins 1.36"),
			wantErr:    "lab.yaml pins 1.36",
			wantStacks: []string{"cluster", "registry"},
		},
		{
			name:       "up starts stopped containers before anything else",
			state:      deployedClusterRegistry,
			args:       []string{"up", "--stacks", "cluster"},
			stopped:    []string{"lab-control-plane", "lab-zot"},
			wantCalls:  []string{"start lab-control-plane", "start lab-zot"},
			wantOut:    []string{"lab-control-plane was stopped; starting it", "lab-zot was stopped; starting it", "nothing to do"},
			wantStacks: []string{"cluster", "registry"},
		},
		{
			name:       "starting them first, then applying",
			args:       []string{"up", "--stacks", "registry"},
			stopped:    []string{"lab-zot"},
			wantCalls:  []string{"start lab-zot", "apply registry"},
			wantOut:    []string{"lab-zot was stopped; starting it", "● registry: applied in"},
			wantStacks: []string{"registry"},
			wantOp:     "lab up --stacks registry",
		},
		{
			name:      "a container that will not start stops lab up",
			args:      []string{"up", "--stacks", "registry"},
			stopped:   []string{"lab-zot"},
			fail:      map[string]bool{"start lab-zot": true},
			wantCalls: []string{"start lab-zot"},
			wantErr:   "starting the lab's stopped containers: boom",
		},
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
			// The fake routes only registry and cluster: the rest is skipped at
			// plan time, so the run applies what it can and succeeds.
			name: "up skips stacks the provider does not support",
			args: []string{"up"},
			wantOut: []string{"- gitops      skip: not supported by the local provider yet",
				"2 to apply, 0 to destroy, 0 unchanged, 3 skipped",
				"warning: 3 stacks skipped, not supported by the local provider yet: gitops, core, sample-app"},
			wantCalls:  []string{"apply registry", "apply cluster"},
			wantStacks: []string{"cluster", "registry"},
			wantOp:     "lab up",
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
			name:       "down --stacks destroys that stack and what needs it",
			state:      deployedClusterRegistry,
			args:       []string{"down", "--stacks", "cluster", "--yes"},
			wantCalls:  []string{"destroy cluster"},
			wantOut:    []string{"- cluster", "selected", "● cluster: destroyed in"},
			wantStacks: []string{"registry"},
			wantOp:     "lab down --stacks cluster",
		},
		{
			name:      "down --stacks of a dependency takes its dependents",
			state:     deployedClusterRegistry,
			args:      []string{"down", "--stacks", "registry", "--yes"},
			wantCalls: []string{"destroy cluster", "destroy registry"},
			wantOut:   []string{"- cluster", "needs registry"},
			wantOp:    "lab down --stacks registry",
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
				cfg = "profile: minimal\nextraStacks: []\n"
			}
			if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.state != "" {
				if err := os.WriteFile(statePath, []byte(tt.state), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fp := &fakeProvider{fail: tt.fail, stopped: tt.stopped, versionErr: tt.versionErr, clusterErr: tt.clusterErr}
			var gotPurge, built bool
			factory := func(_ config.Config, home string, opts runOptions) (provider, error) {
				built, gotPurge = true, opts.Purge
				if opts.Timeout != 30*time.Minute && !slices.Contains(tt.args, "--timeout") {
					t.Errorf("timeout = %s, want the 30m default", opts.Timeout)
				}
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
		{"cloud", "cloud mode needs the repo"},
	}
	for _, tt := range tests {
		cfg := config.Default()
		cfg.Mode = tt.mode
		_, err := defaultProvider(cfg, t.TempDir(), runOptions{})
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%s: err = %v, want %q", tt.mode, err, tt.wantErr)
		}
	}
}

func TestDefaultProviderRoutes(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir()) // the Podman socket is only dialled when used
	tests := []struct {
		gitops     string
		wantGitOps bool
	}{
		{"argocd", true},
		{"flux", false},
	}
	for _, tt := range tests {
		cfg := config.Default()
		cfg.GitOps = tt.gitops
		p, err := defaultProvider(cfg, t.TempDir(), runOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"registry", "cluster"} {
			if !p.Supports(s) {
				t.Errorf("%s: %s not routed", tt.gitops, s)
			}
		}
		for _, s := range []string{"gitops", "core", "obs", "sample-app"} {
			if p.Supports(s) != tt.wantGitOps {
				t.Errorf("%s: Supports(%s) = %v, want %v", tt.gitops, s, p.Supports(s), tt.wantGitOps)
			}
		}
		if p.Supports("secrets") {
			t.Errorf("%s: secrets should not be routed yet", tt.gitops)
		}
	}
}

const deployedWithCore = `{"version":1,"mode":"local","revision":"main","stacks":{
	"registry":{"status":"applied"},"cluster":{"status":"applied"},
	"gitops":{"status":"applied"},"core":{"status":"applied"}},"ops":[]}`

func TestUpPrintsApps(t *testing.T) {
	tests := []struct {
		name    string
		state   string
		urls    []string
		urlsErr error
		want    []string
		wantNot []string
	}{
		{"core applied lists URLs and the tunnel", deployedWithCore,
			[]string{"https://argocd.localtest.me:8443", "https://shop.localtest.me:8443"},
			nil, []string{"nothing to do", "Apps:\n  https://argocd.localtest.me:8443\n  https://shop.localtest.me:8443\nOn the Mac, run ssh -N fedora-lab first.\n"}, nil},
		{"no routes yet", deployedWithCore, nil, nil,
			[]string{"Apps:\n  none yet (no HTTPRoutes on the lab Gateway)\nOn the Mac, run ssh -N fedora-lab first."}, nil},
		{"listing fails is a warning", deployedWithCore, nil, errors.New("connection refused"),
			[]string{"warning: could not list app URLs: connection refused"}, []string{"Apps:"}},
		{"without core there is no Gateway", deployedClusterRegistry, []string{"https://x.localtest.me:8443"}, nil,
			nil, []string{"Apps:", "fedora-lab"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath, statePath := filepath.Join(dir, "lab.yaml"), filepath.Join(dir, "state.json")
			if err := os.WriteFile(cfgPath, []byte("profile: minimal\nextraStacks: []\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(statePath, []byte(tt.state), 0o600); err != nil {
				t.Fatal(err)
			}
			fp := &fakeProvider{urls: tt.urls, urlsErr: tt.urlsErr}
			cmd := newRootCmdWith(func(config.Config, string, runOptions) (provider, error) { return fp, nil })
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"up", "--stacks", "cluster", "--config", cfgPath, "--state", statePath})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}
			for _, w := range tt.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, out.String())
				}
			}
			for _, w := range tt.wantNot {
				if strings.Contains(out.String(), w) {
					t.Errorf("output should not contain %q:\n%s", w, out.String())
				}
			}
		})
	}
}

func TestLabRunnerAppURLs(t *testing.T) {
	url := func(h string) string { return "https://" + h + ":8443" }
	tests := []struct {
		name    string
		l       labRunner
		want    []string
		wantErr string
	}{
		{"no GitOps runner", labRunner{url: url}, nil, ""},
		{"hosts become URLs", labRunner{url: url, hosts: func(context.Context) ([]string, error) {
			return []string{"argocd.localtest.me"}, nil
		}}, []string{"https://argocd.localtest.me:8443"}, ""},
		{"error passes through", labRunner{url: url, hosts: func(context.Context) ([]string, error) {
			return nil, errors.New("no cluster")
		}}, nil, "no cluster"},
	}
	for _, tt := range tests {
		got, err := tt.l.AppURLs(context.Background())
		if (tt.wantErr == "") != (err == nil) || !slices.Equal(got, tt.want) {
			t.Errorf("%s: got %v, %v; want %v, %q", tt.name, got, err, tt.want, tt.wantErr)
		}
	}
}

// While another lab holds the lab, commands that change it (up, down) are
// refused at once, naming the holder, without building a provider or
// printing a plan; read-only ones (plan, status, doctor) run beside it.
func TestOnlyChangesTakeTheLock(t *testing.T) {
	dir := t.TempDir()
	cfgPath, statePath := filepath.Join(dir, "lab.yaml"), filepath.Join(dir, "state.json")
	if err := os.WriteFile(cfgPath, []byte("profile: minimal\nextraStacks: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(deployedClusterRegistry), 0o600); err != nil {
		t.Fatal(err)
	}
	const holder = "the TUI (pid 42 on /dev/pts/3, since 17:20)"
	unlock, err := state.NewStore(statePath).LockLab(holder)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	// doctor and the default state path use ~/.lab, which TestMain points at
	// a temp HOME; hold that one too, so nothing slips past on it.
	home, _ := state.DefaultPath()
	unlockHome, err := state.NewStore(home).LockLab(holder)
	if err != nil {
		t.Fatal(err)
	}
	defer unlockHome()
	withState := func(args ...string) []string { return append(args, "--config", cfgPath, "--state", statePath) }
	run := func(args []string) (string, bool, error) {
		built := false
		cmd := newRootCmdWith(func(config.Config, string, runOptions) (provider, error) { built = true; return &fakeProvider{}, nil })
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), built, err
	}

	for _, args := range [][]string{withState("up"), withState("up", "--stacks", "cluster"), withState("down", "--yes")} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			out, built, err := run(args)
			want := "lab and its components are already running: " + holder + ". Only one lab changes things at a time"
			if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "lab status, lab plan and lab doctor still work") {
				t.Fatalf("err = %v, want %q", err, want)
			}
			if built || out != "" {
				t.Errorf("provider built %v, output %q; want neither", built, out)
			}
		})
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{withState("plan"), "= cluster"},
		{withState("status"), "registry"},
		{[]string{"doctor", "--json"}, `"name"`},
		{[]string{"--version"}, "lab version"},
		{[]string{"up", "--help"}, "Usage:"},
	} {
		t.Run(tc.args[0], func(t *testing.T) {
			out, _, err := run(tc.args)
			if tc.args[0] == "doctor" {
				err = nil // doctor fails checks on a CI runner; it must still run and print
			}
			if err != nil || !strings.Contains(out, tc.want) {
				t.Errorf("%v beside a held lab: %v, want output with %q:\n%s", tc.args, err, tc.want, out)
			}
		})
	}
}

// The holder text names the command and its flags, not --config or --state.
func TestLabHolder(t *testing.T) {
	root := newRootCmd()
	up, _, err := root.Find([]string{"up"})
	if err != nil {
		t.Fatal(err)
	}
	if err := up.ParseFlags([]string{"--stacks", "obs,core", "--prune", "--state", "/x/state.json"}); err != nil {
		t.Fatal(err)
	}
	got := labHolder(up)
	if !strings.HasPrefix(got, "lab up --prune --stacks obs,core (pid ") || !strings.Contains(got, ", since ") || strings.Contains(got, "/dev/null") {
		t.Errorf("holder = %q", got)
	}
	if got := labHolder(root); !strings.HasPrefix(got, "the TUI (pid ") {
		t.Errorf("root holder = %q", got)
	}
}
