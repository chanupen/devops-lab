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
	"testing"

	"github.com/chanupen/devops-lab/internal/doctor"
	"github.com/chanupen/devops-lab/internal/provider/local"
	"github.com/chanupen/devops-lab/internal/state"
	"github.com/chanupen/devops-lab/internal/tui"
)

func TestDoctorFixReports(t *testing.T) {
	for _, tt := range []struct {
		name        string
		stopped     string
		hostFailure bool
		wantFail    bool
	}{
		{"mixed", "", false, true},
		{"stopped", "lab", false, false},
		{"stopped-host-failure", "lab", true, true},
		{"running-api-failure", "running", false, true},
		{"healthy", "", false, false},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v", tt.name, asJSON), func(t *testing.T) {
				f := newGuidedFixture(t)
				if tt.stopped != "" {
					apiCalls := stoppedGuidedFixture(t, f, tt.stopped, tt.hostFailure)
					defer func() {
						if (apiCalls.Load() == 0) != (tt.stopped == "lab") {
							t.Fatalf("API calls=%d", apiCalls.Load())
						}
					}()
				} else {
					st := fixtureReadyState()
					st.Stacks["policy"] = state.StackState{Status: state.Applied}
					f.save(t, st, false)
					f.deps.checks = func() []doctor.Check {
						var checks []doctor.Check
						for _, c := range doctor.Checks() {
							if c.Name == "lab registry" || (tt.name == "mixed" && slices.Contains([]string{"podman", "memory", "k8s support"}, c.Name)) {
								checks = append(checks, c)
							}
						}
						return checks
					}
					f.deps.system = func(home string, readonly bool) doctor.System {
						if home != f.dir || !readonly {
							t.Fatal("fix report selected a mutating probe or the wrong lab")
						}
						return doctor.System{
							ReadFile: func(string) ([]byte, error) {
								return []byte("MemTotal: 25165824 kB\nMemAvailable: 1048576 kB\n"), nil
							},
							LookPath: func(string) (string, error) { return "", errors.New("fixture missing") },
							Run: func(context.Context, string, ...string) (string, error) {
								t.Fatal("a command ran for missing Podman")
								return "", nil
							},
							LabRegistry: func(context.Context) doctor.RegistryState {
								return doctor.RegistryState{WriteUntested: true}
							},
						}
					}
				}
				originalSystem := f.deps.system
				f.deps.system = func(home string, readonly bool) doctor.System {
					if !readonly {
						t.Fatal("fix must use the observational system")
					}
					sys := originalSystem(home, readonly)
					sys.Run = func(context.Context, string, ...string) (string, error) {
						t.Fatal("unexpected external command")
						return "", nil
					}
					return sys
				}
				before, _ := os.ReadFile(f.statePath)
				info, _ := os.Stat(f.statePath)
				configBefore, _ := os.ReadFile(f.cfgPath)
				unlock, err := state.NewStore(f.statePath).LockLab("fixture actor")
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
				files, _ := os.ReadDir(f.dir)
				args := []string{"doctor", "--fix"}
				if asJSON {
					args = append(args, "--json")
				}
				out, stderr, err := f.execute(args...)
				var exit exitError
				if tt.wantFail {
					if !errors.As(err, &exit) || exit.code != 1 {
						t.Fatalf("err=%v:\n%s", err, out)
					}
				} else if err != nil {
					t.Fatalf("err=%v:\n%s", err, out)
				}
				if asJSON {
					var report struct {
						OK           bool `json:"ok"`
						Remediations []struct {
							Check string `json:"check"`
						} `json:"remediations"`
					}
					if err := json.Unmarshal([]byte(out), &report); err != nil || report.OK == tt.wantFail {
						t.Fatalf("report=%+v err=%v:\n%s", report, err, out)
					}
					for _, command := range report.Remediations {
						if command.Check == "k8s support" {
							t.Fatal("allowed warning became remediation")
						}
					}
				} else if tt.name == "mixed" && (!strings.Contains(out, "sudo required") || strings.Contains(out, "WARN k8s support")) {
					t.Fatal(out)
				}
				if tt.stopped == "lab" && (!strings.Contains(out, "lab start --profile minimal") || strings.Contains(out, "fixture API server unreachable")) {
					t.Fatal(out)
				}
				after, _ := os.ReadFile(f.statePath)
				afterInfo, _ := os.Stat(f.statePath)
				configAfter, _ := os.ReadFile(f.cfgPath)
				afterFiles, _ := os.ReadDir(f.dir)
				if !bytes.Equal(before, after) || !info.ModTime().Equal(afterInfo.ModTime()) ||
					!bytes.Equal(configBefore, configAfter) || len(files) != len(afterFiles) {
					t.Fatal("fix report wrote files")
				}
				if f.factories != 0 || len(f.prov.calls) != 0 || len(f.prefetch.stacks) != 0 {
					t.Fatal("fix report reached a provider or downloaded charts")
				}
				name := tt.name
				if asJSON {
					name += "-json"
				}
				got := strings.ReplaceAll(out+"--- stderr ---\n"+stderr, f.dir, "<fixture>")
				path := filepath.Join("testdata", "doctor", name+".golden")
				if os.Getenv("UPDATE_DOCTOR_GOLDENS") == "1" {
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(path)
				if err != nil || got != string(want) {
					t.Fatalf("golden mismatch (%v):\n%s", err, got)
				}
			})
		}
	}
}

func TestDoctorFixExitPolicy(t *testing.T) {
	for _, status := range []doctor.Status{doctor.OK, doctor.Warn, doctor.Fail, doctor.Skip, doctor.Stopped} {
		for _, fix := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fix=%v", status, fix), func(t *testing.T) {
				f := newGuidedFixture(t)
				f.deps.system = func(string, bool) doctor.System { return doctor.System{} }
				f.deps.checks = func() []doctor.Check {
					return []doctor.Check{{Name: "fixture", Run: func(context.Context, doctor.System, doctor.Options) doctor.Result {
						return doctor.Result{Status: status, Detail: "fixture"}
					}}}
				}
				args := []string{"doctor", "--json"}
				if fix {
					args = append(args, "--fix")
				}
				_, _, err := f.execute(args...)
				if (err != nil) != (status == doctor.Fail) {
					t.Fatalf("status=%s fix=%v err=%v", status, fix, err)
				}
				if _, err := os.Stat(f.statePath); !os.IsNotExist(err) {
					t.Fatalf("missing state was created: %v", err)
				}
				if _, err := os.Stat(f.statePath + ".lab.lock"); !os.IsNotExist(err) {
					t.Fatalf("lab lock was created: %v", err)
				}
			})
		}
	}
}

func TestDoctorFixRefusals(t *testing.T) {
	for _, name := range []string{"darwin", "yes", "corrupt state", "future state", "corrupt e2e", "bad config"} {
		t.Run(name, func(t *testing.T) {
			f := newGuidedFixture(t)
			args := []string{"doctor", "--fix", "--json"}
			switch name {
			case "darwin":
				f.deps.goos = "darwin"
			case "yes":
				args = append(args, "--yes")
			case "corrupt state", "future state", "corrupt e2e":
				path, content := f.statePath, "{"
				if name == "future state" {
					content = `{"version":99}`
				}
				if name == "corrupt e2e" {
					path = state.E2EPath(path)
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			case "bad config":
				if err := os.WriteFile(f.cfgPath, []byte("invalid: ["), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			out, _, err := f.execute(args...)
			if err == nil || out != "" {
				t.Fatalf("out=%q err=%v", out, err)
			}
			if name == "darwin" {
				var exit exitError
				if !errors.As(err, &exit) || exit.code != 2 {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestDoctorFixLegacyStateReadOnly(t *testing.T) {
	f := newGuidedFixture(t)
	f.save(t, fixtureReadyState(), false)
	content, err := os.ReadFile(f.statePath)
	if err != nil {
		t.Fatal(err)
	}
	content = bytes.Replace(content, []byte(`"version": 2`), []byte(`"version": 1`), 1)
	if !bytes.Contains(content, []byte(`"version": 1`)) {
		t.Fatal("missing legacy version fixture")
	}
	if err := os.WriteFile(f.statePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(f.statePath)
	out, _, err := f.execute("doctor", "--fix", "--json")
	if err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	after, _ := os.ReadFile(f.statePath)
	afterInfo, _ := os.Stat(f.statePath)
	if !bytes.Equal(content, after) || !info.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("fix report persisted a migration")
	}
}

func TestRequireLinux(t *testing.T) {
	tests := []struct {
		goos     string
		wantCode int // 0 means no error
	}{
		{"linux", 0},
		{"darwin", 2},
		{"windows", 2},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			var stderr bytes.Buffer
			err := requireLinux(tt.goos, &stderr)
			if tt.wantCode == 0 {
				if err != nil || stderr.Len() > 0 {
					t.Fatalf("err = %v, stderr = %q; want neither", err, stderr.String())
				}
				return
			}
			var exit exitError
			if !errors.As(err, &exit) || exit.code != tt.wantCode {
				t.Fatalf("err = %v, want exit code %d", err, tt.wantCode)
			}
			if !strings.Contains(stderr.String(), "ssh -t fedora lab doctor") {
				t.Errorf("stderr = %q, want the ssh hint", stderr.String())
			}
		})
	}
}

// The TUI and lab doctor get the main cluster, scratch clusters and the
// image cache apart, each with the command that starts it.
func TestUnitAdapters(t *testing.T) {
	units := local.Units([]local.ContainerInfo{
		{Name: "lab-control-plane"}, {Name: "lab-worker", Running: true},
		{Name: "lab-scratch-01-x-control-plane", Running: true},
		{Name: "lab-zot"},
	})
	tests := []struct {
		i       int
		kind    string
		name    string
		running bool
		fix     string
	}{
		{0, tui.UnitCluster, "lab", false, "podman start lab-control-plane"},
		{1, tui.UnitScratch, "lab-scratch-01-x", true, ""},
		{2, tui.UnitCache, "lab-zot", false, "podman start lab-zot"},
	}
	got := toTUIUnits(units)
	if len(got) != len(tests) {
		t.Fatalf("units = %+v", got)
	}
	for _, tt := range tests {
		u := got[tt.i]
		if u.Kind != tt.kind || u.Name != tt.name || u.Running != tt.running || (!tt.running && u.Fix != tt.fix) {
			t.Errorf("unit %d = %+v, want %s %s running=%v %q", tt.i, u, tt.kind, tt.name, tt.running, tt.fix)
		}
	}
	// Without Podman, lab doctor skips the container checks.
	if _, err := doctorUnits(context.Background()); err == nil {
		t.Error("doctorUnits reached a Podman socket in a test")
	}
}

func TestDrillState(t *testing.T) {
	dir := t.TempDir()
	main, e2e := filepath.Join(dir, "state.json"), state.E2EPath(filepath.Join(dir, "state.json"))
	if e2e != filepath.Join(dir, "e2e", "state.json") {
		t.Fatalf("E2EPath = %s", e2e)
	}
	if d := drillState(main, e2e); d != nil {
		t.Fatalf("no state files: %+v", d)
	}
	set := func(path string, r *state.DrillRun) {
		t.Helper()
		if err := state.NewStore(path).Update(func(s *state.State) error { s.ActiveDrill = r; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	set(e2e, &state.DrillRun{Drill: "dns", Phase: state.DrillInjecting, Test: true})
	d := drillState(main, e2e)
	if d == nil || d.Label != "e2e test drill dns" || d.Phase != state.DrillInjecting ||
		!strings.Contains(d.Fix, "lab up") || !strings.Contains(d.Fix, "--state "+e2e) {
		t.Fatalf("e2e drill: %+v", d)
	}
	set(main, &state.DrillRun{Drill: "oom", Phase: state.DrillRunning})
	if d := drillState(main, e2e); d == nil || d.Label != "drill oom" || d.Fix != "" {
		t.Fatalf("the lab's own drill comes first: %+v", d)
	}
}

// lab doctor expects the model the ai stack's smoke test uses.
func TestDoctorAIModel(t *testing.T) {
	if doctor.AIModel != local.OllamaModel {
		t.Errorf("doctor expects %s, the ai stack uses %s", doctor.AIModel, local.OllamaModel)
	}
}

// lab doctor reads --config and --state like plan, and takes its profile
// and extra stacks from lab.yaml unless --profile overrides them.
func TestDoctorConfigProfile(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		args        []string
		wantProfile string // JSON profile
		wantDetail  string // memory check detail
	}{
		{"profile and extras from lab.yaml", "profile: minimal\nextraStacks: [obs]\n", nil, "minimal", "minimal + obs needs ~4.6 GiB"},
		{"standard from lab.yaml", "profile: standard\nextraStacks: []\n", nil, "standard", "standard needs ~12 GiB"},
		{"defaults when lab.yaml leaves them out", "mode: local\n", nil, "minimal", "minimal + obs needs ~4.6 GiB"},
		{"--profile overrides lab.yaml", "profile: minimal\nextraStacks: [obs]\n", []string{"--profile", "full"}, "full", "full needs ~19 GiB"},
		{"--profile drops lab.yaml extras", "profile: standard\nextraStacks: [ai]\n", []string{"--profile", "minimal"}, "minimal", "minimal needs ~4 GiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newGuidedFixture(t)
			if err := os.WriteFile(f.cfgPath, []byte(tt.config), 0o600); err != nil {
				t.Fatal(err)
			}
			st := fixtureReadyState()
			st.Stacks["policy"] = state.StackState{Status: state.Applied}
			f.save(t, st, false)
			fixture := f.deps.system
			f.deps.system = func(home string, _ bool) doctor.System {
				return fixture(home, true) // asserts home is --state's directory
			}
			var gotStacks []string
			f.deps.checks = func() []doctor.Check {
				spy := doctor.Check{Name: "stacks", Run: func(_ context.Context, _ doctor.System, o doctor.Options) doctor.Result {
					gotStacks = o.Stacks
					return doctor.Result{Status: doctor.OK}
				}}
				for _, c := range doctor.Checks() {
					if c.Name == "memory" {
						return []doctor.Check{c, spy}
					}
				}
				t.Fatal("no memory check")
				return nil
			}
			out, _, err := f.execute(append([]string{"doctor", "--json"}, tt.args...)...)
			if err != nil {
				t.Fatalf("err=%v\n%s", err, out)
			}
			var report struct {
				Profile string
				Results []struct{ Name, Detail string }
			}
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if report.Profile != tt.wantProfile {
				t.Errorf("profile = %q, want %q", report.Profile, tt.wantProfile)
			}
			if len(report.Results) == 0 || !strings.Contains(report.Results[0].Detail, tt.wantDetail) {
				t.Errorf("memory = %+v, want detail %q", report.Results, tt.wantDetail)
			}
			if !slices.Contains(gotStacks, "policy") {
				t.Errorf("stacks = %v: --state was not read", gotStacks)
			}
		})
	}
}
