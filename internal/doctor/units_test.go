package doctor

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// A cluster or image cache that a reboot stopped shows as ○ stopped, with
// the command that starts it; it is not a failure.
func TestUnitChecks(t *testing.T) {
	main := Unit{Kind: "cluster", Name: "lab", Nodes: 3, Running: true}
	zot := Unit{Kind: "image cache", Name: "lab-zot", Nodes: 1, Running: true}
	scratch := Unit{Kind: "cluster", Name: "lab-scratch-01-x", Nodes: 1, Running: true}
	down := func(u Unit, fix string) Unit { u.Running, u.Fix = false, fix; return u }
	tests := []struct {
		name  string
		units []Unit
		err   error
		check func(context.Context, System, Options) Result
		want  Status
		text  string // in Detail, then Fix
	}{
		{"cluster running", []Unit{main}, nil, unitCheck("cluster", "lab"), OK, "running (3 nodes)"},
		{"cluster stopped", []Unit{down(main, "podman start lab-control-plane lab-worker lab-worker2")}, nil,
			unitCheck("cluster", "lab"), Stopped, "stopped|podman start lab-control-plane lab-worker lab-worker2 (or lab up)"},
		{"cluster not created", nil, nil, unitCheck("cluster", "lab"), Skip, "not created"},
		{"zot running", []Unit{zot}, nil, unitCheck("image cache", "lab-zot"), OK, "running"},
		{"zot stopped", []Unit{main, down(zot, "podman start lab-zot")}, nil, unitCheck("image cache", "lab-zot"),
			Stopped, "stopped|podman start lab-zot (or lab up)"},
		{"podman unreachable", nil, errors.New("dial unix: no such file"), unitCheck("cluster", "lab"), Skip, "podman API unavailable"},
		{"no scratch cluster", []Unit{main}, nil, checkScratchClusters, Skip, "none"},
		{"scratch running", []Unit{main, scratch}, nil, checkScratchClusters, OK, "running: lab-scratch-01-x"},
		{"scratch stopped", []Unit{main, down(scratch, "podman start lab-scratch-01-x-control-plane")}, nil, checkScratchClusters,
			Stopped, "stopped: lab-scratch-01-x|podman start lab-scratch-01-x-control-plane (or lab labs start <id>)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sys := System{Units: func(context.Context) ([]Unit, error) { return tt.units, tt.err }}
			r := tt.check(context.Background(), sys, Options{})
			detail, fix, _ := strings.Cut(tt.text, "|")
			if r.Status != tt.want || !strings.Contains(r.Detail, detail) || !strings.Contains(r.Fix, fix) {
				t.Errorf("got %s %q fix %q; want %s %q fix %q", r.Status, r.Detail, r.Fix, tt.want, detail, fix)
			}
		})
	}
	if r := unitCheck("cluster", "lab")(context.Background(), System{}, Options{}); r.Status != Skip {
		t.Errorf("without Units: %+v", r)
	}
}

func TestWriteTableStopped(t *testing.T) {
	var buf bytes.Buffer
	results := []Result{
		{Name: "lab cluster", Status: OK, Detail: "running (3 nodes)"},
		{Name: "image cache", Status: Stopped, Detail: "stopped", Fix: "podman start lab-zot (or lab up)"},
	}
	if err := WriteTable(&buf, lipgloss.NewRenderer(&buf), results); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"○ image cache  stopped", "fix: podman start lab-zot (or lab up)", "1 ok, 0 warn, 0 fail, 1 stopped"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
	if Failed(results) {
		t.Error("a stopped unit failed lab doctor")
	}
}
