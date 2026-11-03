package local

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestUnits(t *testing.T) {
	kind := func(cluster string) map[string]string { return map[string]string{kindClusterLabel: cluster} }
	tests := []struct {
		name string
		in   []ContainerInfo
		want []Unit
	}{
		{"nothing", nil, []Unit{}},
		{"after a reboot: everything stopped", []ContainerInfo{
			{Name: "lab-control-plane", Labels: kind("lab")},
			{Name: "lab-scratch-01-x-control-plane", Labels: kind("lab-scratch-01-x")},
			{Name: "lab-worker", Labels: kind("lab")},
			{Name: "lab-zot"},
		}, []Unit{
			{Kind: "cluster", Name: "lab", Containers: []string{"lab-control-plane", "lab-worker"}, Stopped: []string{"lab-control-plane", "lab-worker"}},
			{Kind: "cluster", Name: "lab-scratch-01-x", Containers: []string{"lab-scratch-01-x-control-plane"}, Stopped: []string{"lab-scratch-01-x-control-plane"}},
			{Kind: "image cache", Name: "lab-zot", Containers: []string{"lab-zot"}, Stopped: []string{"lab-zot"}},
		}},
		{"one node of two stopped; nodes without the kind label", []ContainerInfo{
			{Name: "lab-control-plane", Running: true},
			{Name: "lab-worker2"},
			{Name: "lab-zot", Running: true},
			{Name: "lab-something"},
		}, []Unit{
			{Kind: "cluster", Name: "lab", Containers: []string{"lab-control-plane", "lab-worker2"}, Stopped: []string{"lab-worker2"}},
			{Kind: "image cache", Name: "lab-zot", Containers: []string{"lab-zot"}},
			{Kind: "container", Name: "lab-something", Containers: []string{"lab-something"}, Stopped: []string{"lab-something"}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Units(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("Units = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				g, w := got[i], tt.want[i]
				if g.Kind != w.Kind || g.Name != w.Name || !slices.Equal(g.Containers, w.Containers) || !slices.Equal(g.Stopped, w.Stopped) {
					t.Errorf("unit %d = %+v, want %+v", i, g, w)
				}
			}
		})
	}
	u := Unit{Stopped: []string{"lab-control-plane", "lab-worker"}}
	if u.Running() || u.Fix() != "podman start lab-control-plane lab-worker" {
		t.Errorf("fix = %q", u.Fix())
	}
}

// lab up starts the main cluster's nodes and the image cache, and waits
// for the nodes when it started them. Scratch nodes and unknown lab-*
// containers stay stopped and are reported with their command.
func TestStartStopped(t *testing.T) {
	all := []string{"lab-control-plane", "lab-worker", "lab-scratch-01-x-control-plane", "lab-zot", "lab-foo"}
	tests := []struct {
		name    string
		stopped []string
		failOn  string
		started []string // started, in order
		waited  int      // nodes waited for; 0: no wait
		logs    []string
		wantErr string
	}{
		{"all running", nil, "", nil, 0, nil, ""},
		{"only the image cache", []string{"lab-zot"}, "", []string{"lab-zot"}, 0,
			[]string{"image cache lab-zot was stopped; starting lab-zot"}, ""},
		{"after a reboot", all, "", []string{"lab-control-plane", "lab-worker", "lab-zot"}, 2, []string{
			"cluster lab was stopped; starting lab-control-plane, lab-worker",
			"scratch cluster lab-scratch-01-x is stopped; lab up leaves it (lab labs start 01-x starts it)",
			"container lab-foo is stopped; lab up leaves it (podman start lab-foo)",
		}, ""},
		{"a stopped scratch node and lab-foo stay stopped", []string{"lab-scratch-01-x-control-plane", "lab-foo"}, "", nil, 0,
			[]string{"lab labs start 01-x starts it", "podman start lab-foo"}, ""},
		{"start fails", []string{"lab-zot"}, "StartContainer", nil, 0, nil, "start lab-zot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newFakeRuntime()
			for _, n := range all {
				rt.containers[n] = ContainerInfo{Exists: true, Running: !slices.Contains(tt.stopped, n)}
			}
			rt.failOn = tt.failOn
			p := newProvider(rt, nil, Options{Kubeconfig: "kc"})
			waited := 0
			p.nodesReady = func(_ context.Context, _ string, want int, _ io.Writer) error { waited = want; return nil }
			var log strings.Builder
			_, err := p.StartStopped(context.Background(), &log)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var started []string
			for _, n := range all {
				running := rt.containers[n].Running
				switch {
				case slices.Contains(tt.started, n):
					if !running {
						t.Errorf("%s still stopped", n)
					}
					started = append(started, n)
				case slices.Contains(tt.stopped, n) && running:
					t.Errorf("%s was started; lab up must leave it", n)
				}
			}
			if !slices.Equal(started, tt.started) || waited != tt.waited {
				t.Errorf("started %v, waited for %d nodes; want %v, %d", started, waited, tt.started, tt.waited)
			}
			if n := strings.Count(rt.callList(), "StartContainer"); n != len(tt.started) {
				t.Errorf("%d StartContainer calls, want %d", n, len(tt.started))
			}
			for _, w := range tt.logs {
				if !strings.Contains(log.String(), w) {
					t.Errorf("log lacks %q:\n%s", w, log.String())
				}
			}
		})
	}
}

// lab labs start starts the image cache and the lab's node when they are
// there but stopped, instead of only warning.
func TestScratchStartsWhatIsStopped(t *testing.T) {
	tests := []struct {
		name     string
		zot      *ContainerInfo
		node     *ContainerInfo
		started  []string
		wantLogs []string
	}{
		{"zot running", &ContainerInfo{Exists: true, Running: true}, nil, nil, []string{"image cache: lab-zot"}},
		{"zot stopped", &ContainerInfo{Exists: true}, nil, []string{"lab-zot"},
			[]string{"image cache lab-zot was stopped; starting it", "image cache: lab-zot"}},
		{"no zot", nil, nil, nil, []string{"images come straight from the registries"}},
		{"node stopped after a reboot", &ContainerInfo{Exists: true}, &ContainerInfo{Exists: true}, []string{"lab-zot", "lab-scratch-01-x-control-plane"},
			[]string{"its node lab-scratch-01-x-control-plane was stopped; starting it"}},
		{"node running", &ContainerInfo{Exists: true, Running: true}, &ContainerInfo{Exists: true, Running: true}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newFakeRuntime()
			if tt.zot != nil {
				rt.containers[ZotContainer] = *tt.zot
			}
			kc := &fakeClusters{nodes: map[string]int{}}
			if tt.node != nil {
				rt.containers["lab-scratch-01-x-control-plane"] = *tt.node
				kc.nodes["lab-scratch-01-x"] = 1
			}
			s, _ := testScratch(t, rt, kc)
			var log strings.Builder
			if err := s.Create(context.Background(), "lab-scratch-01-x", s.home+"/kc", &log); err != nil {
				t.Fatal(err)
			}
			// Started: running now, but not before.
			var started []string
			for n, before := range map[string]*ContainerInfo{ZotContainer: tt.zot, "lab-scratch-01-x-control-plane": tt.node} {
				if before != nil && !before.Running && rt.containers[n].Running {
					started = append(started, n)
				}
			}
			slices.Sort(started)
			slices.Sort(tt.started)
			if !slices.Equal(started, tt.started) || strings.Count(rt.callList(), "StartContainer") != len(tt.started) {
				t.Errorf("started %v (calls %s), want %v", started, rt.callList(), tt.started)
			}
			for _, w := range tt.wantLogs {
				if !strings.Contains(log.String(), w) {
					t.Errorf("log lacks %q:\n%s", w, log.String())
				}
			}
		})
	}
}
