package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/doctor"
	"github.com/chanupen/devops-lab/internal/drills"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/state"
)

type guidedFixture struct {
	dir, cfgPath, statePath string
	deps                    guidedDeps
	prov                    *fakeProvider
	prefetch                *prefetchProvider
	memoryReads, factories  int
	onFactory               func()
}

func newGuidedFixture(t *testing.T) *guidedFixture {
	t.Helper()
	f := &guidedFixture{dir: t.TempDir(), prov: &fakeProvider{}}
	f.prefetch = &prefetchProvider{fakeProvider: f.prov}
	f.cfgPath, f.statePath = filepath.Join(f.dir, "lab.yaml"), filepath.Join(f.dir, "state.json")
	if err := os.WriteFile(f.cfgPath, []byte("profile: minimal\nextraStacks: [obs]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, s := range engine.Catalog() {
		f.prov.also = append(f.prov.also, s.Name)
	}
	f.deps = hostGuidedDeps()
	f.deps.goos = "linux"
	f.deps.now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }
	f.deps.system = func(home string, readonly bool) doctor.System {
		if home != f.dir || !readonly {
			t.Errorf("system: home=%s readonly=%v", home, readonly)
		}
		return doctor.System{
			ReadFile: func(path string) ([]byte, error) {
				if path != "/proc/meminfo" {
					t.Errorf("unexpected read: %s", path)
				}
				f.memoryReads++
				return []byte("MemTotal: 25165824 kB\nMemAvailable: 8388608 kB\n"), nil
			},
			LabRegistry: func(context.Context) doctor.RegistryState {
				return doctor.RegistryState{Missing: true, WriteUntested: true}
			},
		}
	}
	f.deps.checks = func() []doctor.Check {
		var checks []doctor.Check
		for _, c := range doctor.Checks() {
			if c.Name == "memory" || c.Name == "lab registry" {
				checks = append(checks, c)
			}
		}
		return checks
	}
	f.deps.lookPath = func(name string) (string, error) { return "/fixtures/" + name, nil }
	f.deps.catalog = func(string) ([]labs.Lab, []drills.Drill, error) { return nil, nil, nil }
	return f
}

func (f *guidedFixture) execute(args ...string) (string, string, error) {
	return f.executeContext(context.Background(), args...)
}

func (f *guidedFixture) executeContext(ctx context.Context, args ...string) (string, string, error) {
	root := newRootCmdWithGuided(func(config.Config, string, runOptions) (provider, error) {
		f.factories++
		if f.onFactory != nil {
			f.onFactory()
		}
		return f.prefetch, nil
	}, f.deps)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append(args, "--config", f.cfgPath, "--state", f.statePath))
	err := root.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), err
}

func (f *guidedFixture) save(t *testing.T, st state.State, e2e bool) {
	t.Helper()
	path := f.statePath
	if e2e {
		path = state.E2EPath(path)
	}
	if err := state.NewStore(path).Update(func(s *state.State) error { *s = st; return nil }); err != nil {
		t.Fatal(err)
	}
}

func fixtureReadyState() state.State {
	st := state.New()
	st.Mode, st.Revision = "local", "main"
	for _, name := range []string{"registry", "cluster", "gitops", "core", "sample-app", "obs"} {
		st.Stacks[name] = state.StackState{Status: state.Applied}
	}
	return st
}

func stoppedGuidedFixture(t *testing.T, f *guidedFixture, stopped string, hostFailure bool) *atomic.Int32 {
	t.Helper()
	st := fixtureReadyState()
	st.Stacks["ai"] = state.StackState{Status: state.Applied}
	st.Stacks["secrets"] = state.StackState{Status: state.Applied}
	f.save(t, st, false)
	var apiCalls atomic.Int32
	f.deps.system = func(string, bool) doctor.System {
		var observed atomic.Bool
		units := []doctor.Unit{
			{Kind: "cluster", Name: "lab", Nodes: 3, Running: true, Version: "v1.36.0", Fix: "podman start lab-control-plane lab-worker lab-worker2"},
			{Kind: "image cache", Name: "lab-zot", Running: true, Fix: "podman start lab-zot"},
			{Kind: "aws emulator", Name: "lab-moto", Running: true},
			{Kind: "cluster", Name: "lab-scratch-example", Running: true},
		}
		for i := range units {
			if stopped == "all" || units[i].Name == stopped {
				units[i].Running = false
			}
		}
		return doctor.System{
			Exists: func(string) bool { return false },
			ReadFile: func(path string) ([]byte, error) {
				switch path {
				case "/proc/meminfo":
					f.memoryReads++
					return []byte("MemTotal: 25165824 kB\nMemAvailable: 8388608 kB\n"), nil
				case "/proc/sys/fs/inotify/max_user_watches":
					if hostFailure {
						return []byte("8192"), nil
					}
					return []byte("524288"), nil
				case "/proc/sys/fs/inotify/max_user_instances":
					return []byte("512"), nil
				default:
					return nil, fmt.Errorf("unexpected read: %s", path)
				}
			},
			Units: func(context.Context) ([]doctor.Unit, error) {
				observed.Store(true)
				if stopped == "unavailable" {
					return nil, errors.New("fixture podman API unavailable")
				}
				return units, nil
			},
			LabRegistry: func(context.Context) doctor.RegistryState {
				return doctor.RegistryState{WriteUntested: true, Stopped: stopped == "all" || stopped == "lab-zot"}
			},
			MotoSecrets: func(context.Context) (int, error) { return 2, nil },
			AI: func(context.Context) (doctor.AIState, error) {
				if !observed.Load() {
					t.Error("API health ran before runtime observations")
				}
				apiCalls.Add(1)
				return doctor.AIState{}, errors.New("fixture API server unreachable")
			},
			GitOps: func(context.Context) (doctor.GitOpsState, error) {
				if !observed.Load() {
					t.Error("API health ran before runtime observations")
				}
				apiCalls.Add(1)
				return doctor.GitOpsState{}, errors.New("fixture API server unreachable")
			},
		}
	}
	f.deps.checks = func() []doctor.Check {
		var checks []doctor.Check
		for _, check := range doctor.Checks() {
			if guidedRuntimeCheck(check.Name) || slices.Contains([]string{
				"memory", "inotify", "ollama in cluster", "ollama model", "langfuse", "gitops revision",
			}, check.Name) {
				checks = append(checks, check)
			}
		}
		return checks
	}
	return &apiCalls
}

func TestGuidedStoppedRuntime(t *testing.T) {
	for _, tt := range []struct {
		name, stopped string
		hostFailure   bool
		wantStart     bool
		deferred      bool
	}{
		{"stopped main cluster", "lab", false, true, true},
		{"stopped cache", "lab-zot", false, true, true},
		{"stopped moto", "lab-moto", false, true, true},
		{"stopped moto without deployed secrets", "lab-moto", false, true, true},
		{"all stopped", "all", false, true, true},
		{"host failure with stopped cluster", "lab", true, false, true},
		{"host failure with stopped cache", "lab-zot", true, false, true},
		{"host failure with stopped moto", "lab-moto", true, false, true},
		{"running cluster with API failure", "", false, false, false},
		{"scratch stop is not main recovery", "lab-scratch-example", false, false, false},
		{"unknown container status", "unavailable", false, false, false},
	} {
		for _, command := range []string{"start", "next"} {
			t.Run(tt.name+"/"+command, func(t *testing.T) {
				f := newGuidedFixture(t)
				apiCalls := stoppedGuidedFixture(t, f, tt.stopped, tt.hostFailure)
				if tt.name == "stopped moto without deployed secrets" {
					st := fixtureReadyState()
					st.Stacks["ai"] = state.StackState{Status: state.Applied}
					f.save(t, st, false)
				}
				before, _ := os.ReadFile(f.statePath)
				info, _ := os.Stat(f.statePath)
				args := []string{command}
				if command == "start" {
					args = append(args, "--dry-run", "--profile", "minimal")
				} else {
					args = append(args, "--json")
				}
				out, _, err := f.execute(args...)
				if command == "start" {
					if (err == nil) != tt.wantStart {
						t.Fatalf("err=%v:\n%s", err, out)
					}
					if tt.deferred && !strings.Contains(out, "cluster health unverified") {
						t.Fatal(out)
					}
					if tt.stopped == "lab" || tt.stopped == "all" {
						if strings.Contains(out, "  applied") || !strings.Contains(out, "health unverified until nodes restart") {
							t.Fatal(out)
						}
					}
					if tt.wantStart && !strings.Contains(out, "Restart proposed after confirmation:") {
						t.Fatal(out)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					var step nextStep
					if err := json.Unmarshal([]byte(out), &step); err != nil {
						t.Fatal(err)
					}
					if (step.Kind == "start") != tt.wantStart {
						t.Fatalf("step: %+v", step)
					}
					if tt.wantStart && !strings.HasPrefix(step.Command, "lab start --profile minimal ") {
						t.Fatal(step.Command)
					}
					if tt.hostFailure && !strings.Contains(step.Reason, "inotify") {
						t.Fatal(step.Reason)
					}
				}
				if (apiCalls.Load() == 0) != tt.deferred {
					t.Fatalf("API calls=%d, deferred=%v", apiCalls.Load(), tt.deferred)
				}
				after, _ := os.ReadFile(f.statePath)
				afterInfo, _ := os.Stat(f.statePath)
				if !bytes.Equal(before, after) || !info.ModTime().Equal(afterInfo.ModTime()) {
					t.Fatal("state changed")
				}
				if len(f.prov.calls) != 0 || len(f.prefetch.stacks) != 0 {
					t.Fatal("read-only command mutated containers or fetched charts")
				}
				if _, err := os.Stat(f.statePath + ".lab.lock"); !os.IsNotExist(err) {
					t.Fatal("read-only command acquired the lab lock")
				}
			})
		}
	}
}

func TestGuidedStoppedRequestedGPU(t *testing.T) {
	for _, requested := range []bool{false, true} {
		for _, command := range []string{"start", "next"} {
			t.Run(fmt.Sprintf("requested=%v/%s", requested, command), func(t *testing.T) {
				f := newGuidedFixture(t)
				stoppedGuidedFixture(t, f, "lab", false)
				f.save(t, fixtureReadyState(), false)
				if requested {
					if err := os.WriteFile(f.cfgPath, []byte("profile: minimal\nextraStacks: [obs, ai]\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				checks := f.deps.checks
				f.deps.checks = func() []doctor.Check {
					out := checks()
					for _, check := range doctor.Checks() {
						if check.Name == "nvidia driver" {
							out = append(out, check)
						}
					}
					return out
				}
				args := []string{command}
				if command == "start" {
					args = append(args, "--dry-run", "--profile", "minimal")
				} else {
					args = append(args, "--json")
				}
				out, _, err := f.execute(args...)
				if command == "start" {
					if (err != nil) != requested {
						t.Fatalf("err=%v:\n%s", err, out)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					var step nextStep
					if err := json.Unmarshal([]byte(out), &step); err != nil {
						t.Fatal(err)
					}
					if (step.Kind == "doctor") != requested {
						t.Fatalf("step: %+v", step)
					}
					if requested && !strings.Contains(step.Reason, "nvidia driver") {
						t.Fatal(step.Reason)
					}
				}
			})
		}
	}
}

func TestStartStoppedRuntimeConsent(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprint(accepted), func(t *testing.T) {
			f := newGuidedFixture(t)
			stoppedGuidedFixture(t, f, "all", false)
			f.prov.stopped = []string{"lab-control-plane", "lab-worker", "lab-worker2", "lab-zot", "lab-moto"}
			f.deps.confirm = func(*cobra.Command, bool, string) error {
				if len(f.prov.calls) != 0 {
					t.Fatal("containers restarted before consent")
				}
				if !accepted {
					return errors.New("fixture declined")
				}
				return nil
			}
			out, _, err := f.execute("start", "--profile", "minimal")
			if accepted {
				if err != nil {
					t.Fatal(err)
				}
				want := []string{"start lab-control-plane", "start lab-worker", "start lab-worker2", "start lab-zot", "start lab-moto"}
				if !slices.Equal(f.prov.calls, want) || !strings.Contains(out, "Next steps:") {
					t.Fatalf("calls=%v:\n%s", f.prov.calls, out)
				}
			} else if err == nil || len(f.prov.calls) != 0 {
				t.Fatalf("err=%v, calls=%v", err, f.prov.calls)
			}
		})
	}
}

func TestStartReadOnlyAndRefusal(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		setup            func(*guidedFixture)
		wantErr, wantOut string
		wantCode         int
	}{
		{"preview", []string{"--dry-run"}, nil, "", "Profile: minimal (automatic)", 0},
		{"explicit preview", []string{"--dry-run", "--profile", "minimal", "--yes"}, nil, "", "6 to apply", 0},
		{"unknown profile", []string{"--dry-run", "--profile", "huge"}, nil, "unknown profile", "", 0},
		{"insufficient explicit memory", []string{"--profile", "full", "--yes"}, nil, "requires at least 19 GiB", "", 0},
		{"no tty", nil, nil, "pass --yes", "", 0},
		{"doctor failed", []string{"--dry-run"}, func(f *guidedFixture) {
			f.deps.checks = func() []doctor.Check {
				return []doctor.Check{{Name: "podman", Run: func(context.Context, doctor.System, doctor.Options) doctor.Result {
					return doctor.Result{Status: doctor.Fail, Detail: "rootless unavailable"}
				}}}
			}
		}, "preflight failed", "rootless unavailable", 0},
		{"cloud", []string{"--dry-run"}, func(f *guidedFixture) {
			if err := os.WriteFile(f.cfgPath, []byte("mode: cloud\ncloud:\n  owner: example\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "", "", 2},
		{"darwin", []string{"--dry-run"}, func(f *guidedFixture) { f.deps.goos = "darwin" }, "", "", 2},
		{"active lab", []string{"--dry-run"}, func(f *guidedFixture) {
			st := state.New()
			st.ActiveLab = "01-argocd-bootstrap"
			f.save(t, st, false)
		}, "a lab is active", "", 0},
		{"active random drill", []string{"--dry-run"}, func(f *guidedFixture) {
			st := state.New()
			st.ActiveDrill = &state.DrillRun{Drill: "dns", Random: true, Error: "restore dns failed"}
			f.save(t, st, false)
		}, "a drill is active", "", 0},
		{"e2e drill", []string{"--dry-run"}, func(f *guidedFixture) {
			st := state.New()
			st.ActiveDrill = &state.DrillRun{Test: true}
			f.save(t, st, true)
		}, "e2e/state.json", "", 0},
		{"bad state", []string{"--dry-run"}, func(f *guidedFixture) {
			if err := os.WriteFile(f.statePath, []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "unexpected end", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newGuidedFixture(t)
			if tt.setup != nil {
				tt.setup(f)
			}
			before, beforeErr := os.ReadFile(f.statePath)
			beforeCfg, _ := os.ReadFile(f.cfgPath)
			info, _ := os.Stat(f.statePath)
			out, _, err := f.execute(append([]string{"start"}, tt.args...)...)
			switch {
			case tt.wantCode != 0:
				var exit exitError
				if !errors.As(err, &exit) || exit.code != tt.wantCode {
					t.Fatalf("err=%v, want code %d", err, tt.wantCode)
				}
				if f.memoryReads != 0 || f.factories != 0 {
					t.Fatal("refusal reached probes/provider")
				}
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err=%v, want %s", err, tt.wantErr)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			if tt.wantOut != "" && !strings.Contains(out, tt.wantOut) {
				t.Errorf("output lacks %s:\n%s", tt.wantOut, out)
			}
			if len(f.prov.calls) != 0 {
				t.Fatalf("mutated provider: %v", f.prov.calls)
			}
			if len(f.prefetch.stacks) != 0 {
				t.Fatalf("preflight fetched charts: %v", f.prefetch.stacks)
			}
			after, afterErr := os.ReadFile(f.statePath)
			if !bytes.Equal(before, after) || (beforeErr == nil) != (afterErr == nil) {
				t.Fatal("state bytes changed")
			}
			if info != nil {
				now, _ := os.Stat(f.statePath)
				if !info.ModTime().Equal(now.ModTime()) {
					t.Fatal("state mtime changed")
				}
			}
			afterCfg, _ := os.ReadFile(f.cfgPath)
			if !bytes.Equal(beforeCfg, afterCfg) {
				t.Fatal("configuration changed")
			}
			if _, err := os.Stat(f.statePath + ".lab.lock"); !os.IsNotExist(err) {
				t.Fatal("preflight acquired lab lock")
			}
			if f.memoryReads > 1 {
				t.Fatal("memory was read more than once")
			}
			if tt.name == "active random drill" && strings.Contains(out, "dns") {
				t.Fatal("random identity leaked")
			}
		})
	}
}

func TestStartProfileThresholds(t *testing.T) {
	for _, tt := range []struct {
		available uint64
		want      string
		blocked   bool
	}{
		{4<<30 - 1024, "minimal", true}, {4 << 30, "minimal", false},
		{12<<30 - 1024, "minimal", false}, {12 << 30, "standard", false},
		{19<<30 - 1024, "standard", false}, {19 << 30, "full", false},
	} {
		t.Run(fmt.Sprint(tt.available), func(t *testing.T) {
			got, err := selectStartProfile(doctor.Memory{Total: 24 << 30, Available: tt.available}, "")
			if got != tt.want || (err != nil) != tt.blocked {
				t.Fatalf("%s %v", got, err)
			}
		})
	}
}

func TestStartConsentAndSharedApply(t *testing.T) {
	for _, name := range []string{"confirmed", "no-op", "declined", "changed config", "changed state", "changed live plan", "busy", "canceled", "download failed", "apply failed", "version refused"} {
		t.Run(name, func(t *testing.T) {
			f := newGuidedFixture(t)
			f.deps.confirm = func(*cobra.Command, bool, string) error { return nil }
			if name == "no-op" {
				f.save(t, fixtureReadyState(), false)
			}
			if name == "declined" {
				f.deps.confirm = func(*cobra.Command, bool, string) error { return exitError{code: 1} }
			}
			if name == "changed config" {
				f.deps.confirm = func(*cobra.Command, bool, string) error {
					return os.WriteFile(f.cfgPath, []byte("profile: standard\n"), 0o600)
				}
			}
			if name == "changed state" {
				f.deps.confirm = func(*cobra.Command, bool, string) error { f.save(t, fixtureReadyState(), false); return nil }
			}
			if name == "changed live plan" {
				f.onFactory = func() {
					if f.factories == 2 {
						f.prov.also = nil
					}
				}
			}
			if name == "busy" {
				unlock, err := state.NewStore(f.statePath).LockLab("fixture holder")
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "canceled" {
				f.deps.confirm = func(*cobra.Command, bool, string) error { cancel(); return nil }
			}
			if name == "apply failed" {
				f.prov.fail = map[string]bool{"apply cluster": true}
			}
			if name == "download failed" {
				f.prefetch.err = errors.New("fixture chart download failed")
			}
			if name == "version refused" {
				f.prov.versionErr = errors.New("version outside compatibility table")
			}
			out, _, err := f.executeContext(ctx, "start", "--yes")
			if name == "confirmed" || name == "no-op" {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out, "Next steps:") {
					t.Fatal("missing next steps")
				}
				if name == "confirmed" {
					if len(f.prefetch.stacks) != 6 {
						t.Fatalf("prefetch: %v", f.prefetch.stacks)
					}
					st, err := state.NewStore(f.statePath).Load()
					if err != nil {
						t.Fatal(err)
					}
					if len(st.Ops) != 6 || !strings.HasPrefix(st.Ops[0].Command, "lab start --profile minimal") {
						t.Fatalf("ops: %+v", st.Ops)
					}
				}
			} else {
				if err == nil {
					t.Fatal("expected refusal")
				}
				if name == "changed live plan" && !strings.Contains(err.Error(), "live discovery changed") {
					t.Fatal(err)
				}
				if strings.Contains(out, "Next steps:") {
					t.Fatal("next steps printed on failure")
				}
				if name != "apply failed" && len(f.prov.calls) != 0 {
					t.Fatalf("unexpected mutation: %v", f.prov.calls)
				}
			}
		})
	}
}

func TestNextPrioritiesAndReadOnly(t *testing.T) {
	for _, tt := range []struct {
		name, kind string
		setup      func(*guidedFixture)
	}{
		{"empty", "start", nil},
		{"failed doctor", "doctor", func(f *guidedFixture) {
			f.deps.checks = func() []doctor.Check {
				return []doctor.Check{{Name: "podman", Run: func(context.Context, doctor.System, doctor.Options) doctor.Result {
					return doctor.Result{Status: doctor.Fail}
				}}}
			}
		}},
		{"unavailable probe", "doctor", func(f *guidedFixture) {
			f.deps.checks = func() []doctor.Check {
				return []doctor.Check{{Name: "podman API", Run: func(context.Context, doctor.System, doctor.Options) doctor.Result {
					return doctor.Result{Status: doctor.Skip, Unavailable: true}
				}}}
			}
		}},
		{"stopped registry", "start", func(f *guidedFixture) {
			f.save(t, fixtureReadyState(), false)
			f.deps.checks = func() []doctor.Check {
				return []doctor.Check{{Name: "lab registry", Run: func(context.Context, doctor.System, doctor.Options) doctor.Result {
					return doctor.Result{Status: doctor.Stopped}
				}}}
			}
		}},
		{"warning before first start", "start", func(f *guidedFixture) {
			f.deps.checks = func() []doctor.Check {
				return []doctor.Check{{Name: "memory", Run: func(context.Context, doctor.System, doctor.Options) doctor.Result {
					return doctor.Result{Status: doctor.Warn}
				}}}
			}
		}},
		{"active lab", "lab", func(f *guidedFixture) {
			st := fixtureReadyState()
			st.ActiveLab = "01-argocd-bootstrap"
			f.save(t, st, false)
		}},
		{"running random drill", "drill", func(f *guidedFixture) {
			st := fixtureReadyState()
			st.ActiveDrill = &state.DrillRun{Drill: "dns", Random: true, Phase: state.DrillRunning, Deadline: f.deps.now().Add(time.Hour)}
			f.save(t, st, false)
		}},
		{"e2e restoring", "restore-drill", func(f *guidedFixture) {
			st := state.New()
			st.ActiveDrill = &state.DrillRun{Phase: state.DrillRestoring}
			f.save(t, st, true)
		}},
		{"warning", "doctor", func(f *guidedFixture) {
			f.save(t, fixtureReadyState(), false)
			f.deps.checks = func() []doctor.Check {
				return []doctor.Check{{Name: "memory", Run: func(context.Context, doctor.System, doctor.Options) doctor.Result {
					return doctor.Result{Status: doctor.Warn}
				}}}
			}
		}},
		{"eligible ordered lab", "start-lab", func(f *guidedFixture) {
			f.save(t, fixtureReadyState(), false)
			f.deps.catalog = func(string) ([]labs.Lab, []drills.Drill, error) {
				return []labs.Lab{{ID: "02-next", Modes: []string{"local"}}, {ID: "01-first", Modes: []string{"local"}}}, nil, nil
			}
		}},
		{"missing tool", "review", func(f *guidedFixture) {
			f.save(t, fixtureReadyState(), false)
			f.deps.lookPath = func(string) (string, error) { return "", errors.New("missing") }
			f.deps.catalog = func(string) ([]labs.Lab, []drills.Drill, error) {
				return []labs.Lab{{ID: "01-first", Modes: []string{"local"}, Tools: []string{"jq"}}}, nil, nil
			}
		}},
		{"unsupported lab mode", "review", func(f *guidedFixture) {
			f.save(t, fixtureReadyState(), false)
			f.deps.catalog = func(string) ([]labs.Lab, []drills.Drill, error) {
				return []labs.Lab{{ID: "06-cloud", Modes: []string{"cloud"}}}, nil, nil
			}
		}},
		{"missing prerequisite", "review", func(f *guidedFixture) {
			f.save(t, fixtureReadyState(), false)
			f.deps.catalog = func(string) ([]labs.Lab, []drills.Drill, error) {
				return []labs.Lab{{ID: "02-next", Modes: []string{"local"}, Stacks: []string{"secrets"}}}, nil, nil
			}
		}},
		{"drill", "start-drill", func(f *guidedFixture) {
			f.save(t, fixtureReadyState(), false)
			f.deps.catalog = func(string) ([]labs.Lab, []drills.Drill, error) {
				return nil, []drills.Drill{{ID: "dns", Stacks: []string{"core"}}}, nil
			}
		}},
		{"all completed", "review", func(f *guidedFixture) {
			st := fixtureReadyState()
			st.Labs = map[string]state.LabProgress{"01-first": {Completed: f.deps.now()}}
			f.save(t, st, false)
			f.deps.catalog = func(string) ([]labs.Lab, []drills.Drill, error) {
				return []labs.Lab{{ID: "01-first", Modes: []string{"local"}}}, nil, nil
			}
		}},
		{"durable lab after scratch cleanup", "review", func(f *guidedFixture) {
			st := fixtureReadyState()
			st.RecordLabCompletion("01-first", f.deps.now(), time.Minute, true)
			f.save(t, st, false)
			f.deps.catalog = func(string) ([]labs.Lab, []drills.Drill, error) {
				return []labs.Lab{{ID: "01-first", Modes: []string{"local"}}}, nil, nil
			}
		}},
		{"durable drill after history trimming", "review", func(f *guidedFixture) {
			st := fixtureReadyState()
			st.ActiveDrill = &state.DrillRun{Drill: "dns", Started: f.deps.now().Add(-time.Minute), Recovered: f.deps.now()}
			st.EndDrill(state.DrillRecovered, f.deps.now())
			st.Drills = nil
			f.save(t, st, false)
			f.deps.catalog = func(string) ([]labs.Lab, []drills.Drill, error) {
				return nil, []drills.Drill{{ID: "dns", Stacks: []string{"core"}}}, nil
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newGuidedFixture(t)
			if tt.setup != nil {
				tt.setup(f)
			}
			before, _ := os.ReadFile(f.statePath)
			unlock, err := state.NewStore(f.statePath).LockLab("other lab")
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			info, _ := os.Stat(f.statePath)
			files, _ := os.ReadDir(f.dir)
			out, stderr, err := f.execute("next", "--json")
			if err != nil {
				t.Fatal(err)
			}
			if stderr != "" {
				t.Fatalf("stderr: %s", stderr)
			}
			var step nextStep
			if err := json.Unmarshal([]byte(out), &step); err != nil {
				t.Fatal(err)
			}
			if step.Kind != tt.kind {
				t.Fatalf("step: %+v", step)
			}
			if tt.name == "eligible ordered lab" && !strings.Contains(step.Command, "01-first") {
				t.Fatal(step.Command)
			}
			if (tt.name == "running random drill" || tt.name == "drill") && strings.Contains(out, "dns") {
				t.Fatal("identity leaked")
			}
			if tt.kind == "doctor" && !strings.Contains(step.Command, "--read-only") {
				t.Fatal("diagnostic uses active probe")
			}
			after, _ := os.ReadFile(f.statePath)
			if !bytes.Equal(before, after) {
				t.Fatal("state changed")
			}
			afterFiles, _ := os.ReadDir(f.dir)
			if len(afterFiles) != len(files) {
				t.Fatal("next created files")
			}
			if info != nil {
				afterInfo, _ := os.Stat(f.statePath)
				if !info.ModTime().Equal(afterInfo.ModTime()) {
					t.Fatal("next changed state mtime")
				}
			}
			if f.factories != 0 || len(f.prov.calls) != 0 {
				t.Fatal("next reached provider")
			}
			if f.memoryReads != 1 {
				t.Fatal("memory read count")
			}
		})
	}
}

func TestNextWarningDisposition(t *testing.T) {
	for _, tt := range []struct {
		name, fix, kind string
		status          doctor.Status
	}{
		{"allowed", "allowed", "start-lab", doctor.Warn},
		{"allowed explanation", "allowed: tested range", "start-lab", doctor.Warn},
		{"accepted", "accepted", "start-lab", doctor.Warn},
		{"accepted explanation", " Accepted: reviewed constraint ", "start-lab", doctor.Warn},
		{"actionable", "update the component", "doctor", doctor.Warn},
		{"unmarked", "", "doctor", doctor.Warn},
		{"not accepted", "not accepted: update the component", "doctor", doctor.Warn},
		{"failure marked allowed", "allowed: tested range", "doctor", doctor.Fail},
		{"allowed before actionable", "allowed: tested range", "doctor", doctor.Warn},
		{"Kyverno tested range", "", "start-lab", doctor.Warn},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newGuidedFixture(t)
			st := fixtureReadyState()
			st.Stacks["policy"] = state.StackState{Status: state.Applied}
			f.save(t, st, false)
			f.deps.catalog = func(string) ([]labs.Lab, []drills.Drill, error) {
				return []labs.Lab{{ID: "01-first", Modes: []string{"local"}, Stacks: []string{"core"}}}, nil, nil
			}
			f.deps.checks = func() []doctor.Check {
				if tt.name == "Kyverno tested range" {
					for _, check := range doctor.Checks() {
						if check.Name == "k8s support" {
							return []doctor.Check{check}
						}
					}
					t.Fatal("missing k8s support check")
				}
				checks := []doctor.Check{{Name: "k8s support", Run: func(context.Context, doctor.System, doctor.Options) doctor.Result {
					return doctor.Result{Status: tt.status, Fix: tt.fix}
				}}}
				if tt.name == "allowed before actionable" {
					checks = append(checks, doctor.Check{Name: "memory", Run: func(context.Context, doctor.System, doctor.Options) doctor.Result {
						return doctor.Result{Status: doctor.Warn, Fix: "close memory-heavy apps"}
					}})
				}
				return checks
			}
			out, _, err := f.execute("next", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var step nextStep
			if err := json.Unmarshal([]byte(out), &step); err != nil {
				t.Fatal(err)
			}
			if step.Kind != tt.kind {
				t.Fatalf("step: %+v", step)
			}
			if tt.kind == "start-lab" && !strings.HasPrefix(step.Command, "lab labs start 01-first ") {
				t.Fatal(step.Command)
			}
			if tt.name == "allowed before actionable" && !strings.Contains(step.Reason, "memory") {
				t.Fatal(step.Reason)
			}
		})
	}
}

func TestGuidedGoldens(t *testing.T) {
	for _, tt := range []struct {
		name       string
		args       []string
		warningFix string
	}{
		{"start-dry-run", []string{"start", "--dry-run", "--profile", "minimal"}, ""},
		{"start-blocked", []string{"start", "--dry-run", "--profile", "full"}, ""},
		{"next", []string{"next"}, ""},
		{"next-json", []string{"next", "--json"}, ""},
		{"next-allowed-warning", []string{"next"}, "allowed: tested range"},
		{"next-actionable-warning", []string{"next"}, "update the component"},
		{"start-stopped", []string{"start", "--dry-run", "--profile", "minimal"}, ""},
		{"start-stopped-host-blocked", []string{"start", "--dry-run", "--profile", "minimal"}, ""},
		{"next-stopped", []string{"next"}, ""},
		{"next-stopped-host-blocked", []string{"next"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newGuidedFixture(t)
			if strings.Contains(tt.name, "stopped") {
				stoppedGuidedFixture(t, f, "all", strings.Contains(tt.name, "host-blocked"))
			}
			if tt.warningFix != "" {
				f.save(t, fixtureReadyState(), false)
				f.deps.catalog = func(string) ([]labs.Lab, []drills.Drill, error) {
					return []labs.Lab{{ID: "01-first", Modes: []string{"local"}}}, nil, nil
				}
				f.deps.checks = func() []doctor.Check {
					return []doctor.Check{{Name: "k8s support", Run: func(context.Context, doctor.System, doctor.Options) doctor.Result {
						return doctor.Result{Status: doctor.Warn, Fix: tt.warningFix}
					}}}
				}
			}
			out, stderr, err := f.execute(tt.args...)
			if err != nil {
				stderr += err.Error() + "\n"
			}
			got := strings.ReplaceAll(out+"--- stderr ---\n"+stderr, f.dir, "<fixture>")
			path := filepath.Join("testdata", "guided", tt.name+".golden")
			if os.Getenv("UPDATE_GUIDED_GOLDENS") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("golden mismatch:\n%s", got)
			}
		})
	}
}

func TestRequestedVersusDeployed(t *testing.T) {
	f := newGuidedFixture(t)
	f.deps.system = func(string, bool) doctor.System {
		return doctor.System{ReadFile: func(string) ([]byte, error) {
			f.memoryReads++
			return []byte("MemTotal: 25165824 kB\nMemAvailable: 20971520 kB\n"), nil
		}}
	}
	var requested, deployed []string
	f.deps.checks = func() []doctor.Check {
		return []doctor.Check{{Name: "fixture", Run: func(_ context.Context, _ doctor.System, o doctor.Options) doctor.Result {
			requested = o.RequestedStacks
			deployed = o.Stacks
			return doctor.Result{Status: doctor.OK}
		}}}
	}
	if _, _, err := f.execute("start", "--dry-run"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(requested, "ai") || slices.Contains(deployed, "ai") {
		t.Fatalf("requested=%v deployed=%v", requested, deployed)
	}
}

func TestNextInputErrorsAndMissingState(t *testing.T) {
	for _, name := range []string{"missing state", "quoted paths", "corrupt state", "future state", "corrupt e2e", "memory unavailable", "catalog unavailable"} {
		t.Run(name, func(t *testing.T) {
			f := newGuidedFixture(t)
			switch name {
			case "quoted paths":
				oldCfg := f.cfgPath
				f.dir = filepath.Join(f.dir, "path with spaces'and-quotes")
				if err := os.Mkdir(f.dir, 0o700); err != nil {
					t.Fatal(err)
				}
				f.cfgPath, f.statePath = filepath.Join(f.dir, "lab.yaml"), filepath.Join(f.dir, "state.json")
				if err := os.Rename(oldCfg, f.cfgPath); err != nil {
					t.Fatal(err)
				}
			case "corrupt state", "future state", "corrupt e2e":
				path, data := f.statePath, "{"
				if name == "future state" {
					data = `{"version":999}`
				}
				if name == "corrupt e2e" {
					path = state.E2EPath(path)
					if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			case "memory unavailable":
				f.deps.system = func(string, bool) doctor.System {
					return doctor.System{ReadFile: func(string) ([]byte, error) { return nil, errors.New("unreadable meminfo") }}
				}
			case "catalog unavailable":
				f.deps.catalog = func(string) ([]labs.Lab, []drills.Drill, error) { return nil, nil, errors.New("unreadable catalog") }
			}
			files, _ := os.ReadDir(f.dir)
			out, _, err := f.execute("next", "--json")
			if name == "missing state" || name == "quoted paths" {
				if err != nil {
					t.Fatal(err)
				}
				var step nextStep
				if err := json.Unmarshal([]byte(out), &step); err != nil {
					t.Fatal(err)
				}
				if name == "quoted paths" && !strings.Contains(step.Command, "--config "+shellArg(f.cfgPath)) {
					t.Fatal(step.Command)
				}
			} else if err == nil || out != "" {
				t.Fatalf("err=%v stdout=%q", err, out)
			}
			after, _ := os.ReadDir(f.dir)
			if len(files) != len(after) {
				t.Fatal("next created files")
			}
			if f.factories != 0 {
				t.Fatal("next constructed a provider")
			}
		})
	}
}
