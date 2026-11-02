package local

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func testScratch(t *testing.T, rt *fakeRuntime, kc *fakeClusters) (*Scratch, *[]string) {
	t.Helper()
	var paths []string
	s := &Scratch{home: t.TempDir(), rt: rt,
		clusters: func(_ io.Writer, kubeconfig string, retain bool) Clusters {
			paths = append(paths, kubeconfig)
			kc.retains = append(kc.retains, retain)
			return kc
		},
		ready: func(context.Context, string, int, io.Writer) error { return nil }}
	return s, &paths
}

// A scratch cluster is one node, pulls through the same mirror files as
// the main lab, publishes no host ports (the main lab owns 8080 and 8443)
// and is written to its own kubeconfig, never ~/.kube/config.
func TestScratchCreate(t *testing.T) {
	rt := newFakeRuntime()
	rt.containers[ZotContainer] = ContainerInfo{Running: true}
	kc := &fakeClusters{nodes: map[string]int{ClusterName: 3}}
	s, paths := testScratch(t, rt, kc)
	kubeconfig := filepath.Join(s.home, "labs", "01-x", "kubeconfig")
	var log strings.Builder
	if err := s.Create(context.Background(), "lab-scratch-01-x", kubeconfig, &log); err != nil {
		t.Fatal(err)
	}
	if len(kc.created) != 1 {
		t.Fatalf("created %d clusters", len(kc.created))
	}
	c := kc.created[0]
	if c.Name != "lab-scratch-01-x" || len(c.Nodes) != 1 || len(c.Nodes[0].ExtraPortMappings) != 0 {
		t.Errorf("config: %s, %d nodes, ports %v", c.Name, len(c.Nodes), c.Nodes[0].ExtraPortMappings)
	}
	if m := c.Nodes[0].ExtraMounts; len(m) != 1 || m[0].HostPath != s.certsDir() || !m[0].Readonly {
		t.Errorf("mounts: %+v", m)
	}
	if _, err := os.Stat(filepath.Join(s.certsDir(), "ghcr.io", "hosts.toml")); err != nil {
		t.Errorf("mirror files: %v", err)
	}
	if b, _ := os.ReadFile(kubeconfig); string(b) != "kubeconfig for lab-scratch-01-x" {
		t.Errorf("kubeconfig = %q", b)
	}
	if len(*paths) != 2 || (*paths)[1] != kubeconfig {
		t.Errorf("kind was given kubeconfig %q, want %q", *paths, kubeconfig)
	}
	if !strings.Contains(log.String(), "image cache: lab-zot") {
		t.Errorf("log: %s", log.String())
	}
	// Again: kept, not created twice.
	if err := s.Create(context.Background(), "lab-scratch-01-x", kubeconfig, io.Discard); err != nil || len(kc.created) != 1 {
		t.Errorf("second create: %v, %d creates", err, len(kc.created))
	}
}

func TestScratchWithoutZot(t *testing.T) {
	s, _ := testScratch(t, newFakeRuntime(), &fakeClusters{nodes: map[string]int{}})
	var log strings.Builder
	if err := s.Create(context.Background(), "lab-scratch-01-x", filepath.Join(s.home, "kc"), &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "images come straight from the registries") {
		t.Errorf("log: %s", log.String())
	}
}

// Labs never create or delete the main lab's cluster.
func TestScratchLeavesTheMainClusterAlone(t *testing.T) {
	kc := &fakeClusters{nodes: map[string]int{ClusterName: 3}}
	s, _ := testScratch(t, newFakeRuntime(), kc)
	if err := s.Delete(context.Background(), ClusterName, filepath.Join(s.home, "kc"), io.Discard); err == nil || len(kc.deleted) != 0 {
		t.Errorf("deleted the main cluster: %v %v", err, kc.deleted)
	}
	if err := s.Create(context.Background(), ClusterName, filepath.Join(s.home, "kc"), io.Discard); err == nil {
		t.Error("created over the main cluster")
	}
	kc.nodes["lab-scratch-01-x"] = 1
	kubeconfig := filepath.Join(s.home, "kc")
	_ = os.WriteFile(kubeconfig, []byte("x"), 0o600)
	if err := s.Delete(context.Background(), "lab-scratch-01-x", kubeconfig, io.Discard); err != nil || len(kc.deleted) != 1 {
		t.Errorf("delete: %v %v", err, kc.deleted)
	}
	if _, err := os.Stat(kubeconfig); !os.IsNotExist(err) {
		t.Errorf("kubeconfig left: %v", err)
	}
	if err := s.Delete(context.Background(), "lab-scratch-01-x", kubeconfig, io.Discard); err != nil {
		t.Errorf("deleting a gone cluster: %v", err)
	}
}

// A failed first create keeps its node (retain), records what Podman knew
// about it, deletes it and tries once more; two failures fail the start.
func TestScratchRetriesCreateOnce(t *testing.T) {
	kc := &fakeClusters{nodes: map[string]int{}, failFirst: 1}
	s, _ := testScratch(t, newFakeRuntime(), kc)
	var diagnosed []string
	s.diagnose = func(_ context.Context, node string, w io.Writer) {
		diagnosed = append(diagnosed, node)
		_, _ = io.WriteString(w, "--- podman logs "+node+" ---\nerror: too many open files\n")
	}
	var log strings.Builder
	if err := s.Create(context.Background(), "lab-scratch-01-x", filepath.Join(s.home, "kc"), &log); err != nil || len(kc.created) != 1 {
		t.Fatalf("after one failure: %v, %d created", err, len(kc.created))
	}
	// List (no retain), first create (retain), second create (no retain).
	if want := []bool{false, true, false}; !slices.Equal(kc.retains, want) {
		t.Errorf("retain per call = %v, want %v", kc.retains, want)
	}
	if !slices.Equal(diagnosed, []string{"lab-scratch-01-x-control-plane"}) || !slices.Equal(kc.deleted, []string{"lab-scratch-01-x"}) {
		t.Errorf("diagnosed %v, deleted %v", diagnosed, kc.deleted)
	}
	l := log.String()
	for _, want := range []string{"creating \"lab-scratch-01-x\" failed: failed to copy kubeadm config", "error: too many open files",
		"removing the failed cluster", "trying once more"} {
		if !strings.Contains(l, want) {
			t.Errorf("log lacks %q:\n%s", want, l)
		}
	}
	if strings.Index(l, "too many open files") > strings.Index(l, "removing the failed cluster") {
		t.Error("diagnostics written after the cluster was removed")
	}
	kc = &fakeClusters{nodes: map[string]int{}, failFirst: 2}
	s, _ = testScratch(t, newFakeRuntime(), kc)
	if err := s.Create(context.Background(), "lab-scratch-01-x", filepath.Join(s.home, "kc"), io.Discard); err == nil {
		t.Error("two failures did not fail the start")
	}
}
