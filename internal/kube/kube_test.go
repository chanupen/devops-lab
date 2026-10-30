package kube

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

// syncBuffer is a bytes.Buffer safe for the informer goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func node(name string, ready corev1.ConditionStatus) *corev1.Node {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if ready != "" {
		n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}
	}
	return n
}

func TestNodesReady(t *testing.T) {
	tests := []struct {
		name       string
		nodes      []*corev1.Node
		want       int
		wantDone   bool
		wantStatus string
	}{
		{"all ready", []*corev1.Node{node("cp", "True"), node("w1", "True"), node("w2", "True")}, 3, true, "nodes ready: 3/3"},
		{"one not ready", []*corev1.Node{node("cp", "True"), node("w2", "False"), node("w1", "True")}, 3, false,
			"nodes ready: 2/3 (waiting for w2)"},
		{"no condition yet counts as not ready", []*corev1.Node{node("cp", "True"), node("w1", "")}, 2, false,
			"nodes ready: 1/2 (waiting for w1)"},
		{"fewer than wanted", []*corev1.Node{node("cp", "True")}, 3, false, "nodes ready: 1/3"},
		{"none yet", nil, 3, false, "nodes ready: 0/3"},
		{"want zero means all present", []*corev1.Node{node("cp", "True")}, 0, true, "nodes ready: 1/1"},
		{"unknown is not ready", []*corev1.Node{node("cp", "Unknown")}, 1, false, "nodes ready: 0/1 (waiting for cp)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var items []any
			for _, n := range tt.nodes {
				items = append(items, n)
			}
			done, status := nodesReady(items, tt.want)
			if done != tt.wantDone || status != tt.wantStatus {
				t.Errorf("got %v %q, want %v %q", done, status, tt.wantDone, tt.wantStatus)
			}
		})
	}
}

func TestWaitNodesReadyWatches(t *testing.T) {
	cs := fake.NewClientset(node("cp", "True"), node("w1", "False"))
	var log syncBuffer
	errc := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { errc <- WaitNodesReady(ctx, cs, 2, &log) }()

	// The wait must not finish until w1 turns Ready.
	select {
	case err := <-errc:
		t.Fatalf("returned early: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := cs.CoreV1().Nodes().UpdateStatus(ctx, node("w1", "True"), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"nodes ready: 1/2 (waiting for w1)", "nodes ready: 2/2"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, log.String())
		}
	}
}

func TestWaitNodesReadyTimeout(t *testing.T) {
	cs := fake.NewClientset(node("cp", "False"))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := WaitNodesReady(ctx, cs, 1, &syncBuffer{})
	if err == nil || !strings.Contains(err.Error(), "waiting for cp") || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("err = %v, want the last status and a deadline error", err)
	}
}

// app builds an Application; compared nil means Argo has not compared yet.
func app(name string, labels map[string]string, sync, health string, src, compared map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]any{"name": name, "namespace": "argocd"},
		"spec":     map[string]any{"source": src},
	}}
	u.SetLabels(labels)
	status := map[string]any{}
	if sync != "" {
		s := map[string]any{"status": sync}
		if compared != nil {
			s["comparedTo"] = map[string]any{"source": compared}
		}
		status["sync"] = s
	}
	if health != "" {
		status["health"] = map[string]any{"status": health}
	}
	u.Object["status"] = status
	return u
}

func TestAppFrom(t *testing.T) {
	src := map[string]any{"repoURL": "git@x:y.git", "path": "gitops/bootstrap",
		"helm": map[string]any{"valuesObject": map[string]any{"stacks": map[string]any{"core": true}}}}
	old := map[string]any{"repoURL": "git@x:y.git", "path": "gitops/bootstrap",
		"helm": map[string]any{"valuesObject": map[string]any{"stacks": map[string]any{"core": false}}}}
	tests := []struct {
		name      string
		u         *unstructured.Unstructured
		wantSync  string
		wantHlth  string
		wantCur   bool
		wantReady bool
	}{
		{"synced healthy current", app("a", nil, "Synced", "Healthy", src, src), "Synced", "Healthy", true, true},
		{"compared against old source", app("a", nil, "Synced", "Healthy", src, old), "Synced", "Healthy", false, false},
		{"progressing", app("a", nil, "Synced", "Progressing", src, src), "Synced", "Progressing", true, false},
		{"out of sync", app("a", nil, "OutOfSync", "Healthy", src, src), "OutOfSync", "Healthy", true, false},
		{"no status yet", app("a", nil, "", "", src, nil), "", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := AppFrom(tt.u)
			if a.Sync != tt.wantSync || a.Health != tt.wantHlth || a.Current != tt.wantCur || a.Ready() != tt.wantReady {
				t.Errorf("got %+v ready=%v", a, a.Ready())
			}
		})
	}
}

func TestWaitAppsWatches(t *testing.T) {
	src := map[string]any{"path": "p"}
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{AppGVR: "ApplicationList"},
		app("cert-manager", map[string]string{"lab.stack": "core"}, "Synced", "Progressing", src, src))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var log syncBuffer
	errc := make(chan error, 1)
	go func() {
		errc <- WaitApps(ctx, dyn, "argocd", func(apps []App) (bool, string) {
			for _, a := range apps {
				if !a.Ready() {
					return false, "waiting for " + a.Name
				}
			}
			return len(apps) > 0, "all ready"
		}, &log)
	}()
	select {
	case err := <-errc:
		t.Fatalf("returned early: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := dyn.Resource(AppGVR).Namespace("argocd").Update(ctx,
		app("cert-manager", map[string]string{"lab.stack": "core"}, "Synced", "Healthy", src, src), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if got := log.String(); !strings.Contains(got, "waiting for cert-manager") || !strings.Contains(got, "all ready") {
		t.Errorf("log = %q", got)
	}
}
