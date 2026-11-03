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

// lab up starts every stopped lab container first, and waits for the main
// cluster's nodes when it started them.
func TestStartStopped(t *testing.T) {
	tests := []struct {
		name    string
		stopped []string
		failOn  string
		started []string // StartContainer calls, in order
		waited  int      // nodes waited for; 0: no wait
		wantErr string
	}{
		{"all running", nil, "", nil, 0, ""},
		{"only the image cache", []string{"lab-zot"}, "", []string{"lab-zot"}, 0, ""},
		{"after a reboot", []string{"lab-control-plane", "lab-worker", "lab-zot"}, "",
			[]string{"lab-control-plane", "lab-worker", "lab-zot"}, 2, ""},
		{"a scratch cluster: no wait for the main one", []string{"lab-scratch-01-x-control-plane"}, "",
			[]string{"lab-scratch-01-x-control-plane"}, 0, ""},
		{"start fails", []string{"lab-zot"}, "StartContainer", nil, 0, "start lab-zot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newFakeRuntime()
			for _, n := range []string{"lab-control-plane", "lab-worker", "lab-scratch-01-x-control-plane", "lab-zot"} {
				rt.containers[n] = ContainerInfo{Exists: true, Running: !slices.Contains(tt.stopped, n)}
			}
			rt.failOn = tt.failOn
			var starts []string
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
			for _, n := range []string{"lab-control-plane", "lab-worker", "lab-scratch-01-x-control-plane", "lab-zot"} {
				if slices.Contains(tt.stopped, n) {
					starts = append(starts, n)
					if !rt.containers[n].Running {
						t.Errorf("%s still stopped", n)
					}
				}
			}
			if !slices.Equal(starts, tt.started) || waited != tt.waited {
				t.Errorf("started %v, waited for %d nodes; want %v, %d", starts, waited, tt.started, tt.waited)
			}
			if n := strings.Count(rt.callList(), "StartContainer"); n != len(tt.started) {
				t.Errorf("%d StartContainer calls, want %d", n, len(tt.started))
			}
			if len(tt.started) > 0 && !strings.Contains(log.String(), "was stopped; starting") {
				t.Errorf("log: %s", log.String())
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
