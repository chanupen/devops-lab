package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chanupen/devops-lab/internal/doctor"
	"github.com/chanupen/devops-lab/internal/provider/local"
	"github.com/chanupen/devops-lab/internal/state"
	"github.com/chanupen/devops-lab/internal/tui"
)

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
