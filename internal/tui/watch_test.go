package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/chanupen/devops-lab/internal/gitops"
	"github.com/chanupen/devops-lab/internal/kube"
)

func node(name string, ready bool) *corev1.Node {
	s := corev1.ConditionFalse
	if ready {
		s = corev1.ConditionTrue
	}
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: s}}}}
}

func application(name, stack string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]any{"name": name, "namespace": gitops.Namespace, "labels": map[string]any{gitops.StackLabel: stack}},
		"spec":     map[string]any{"source": map[string]any{"path": name}},
		"status": map[string]any{
			"sync":   map[string]any{"status": "Synced", "comparedTo": map[string]any{"source": map[string]any{"path": name}}},
			"health": map[string]any{"status": "Healthy"},
		},
	}}
}

func route(host string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "HTTPRoute",
		"metadata": map[string]any{"name": host, "namespace": "monitoring"},
		"spec": map[string]any{
			"hostnames":  []any{host},
			"parentRefs": []any{map[string]any{"name": gitops.GatewayName, "namespace": gitops.GatewayNamespace}},
		},
	}}
}

func fakeClients(nodes []runtime.Object, objs ...runtime.Object) *kube.Clients {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{kube.AppGVR: "ApplicationList", gitops.HTTPRouteGVR: "HTTPRouteList"}, objs...)
	return &kube.Clients{Core: fake.NewClientset(nodes...), Dynamic: dyn}
}

// next waits for a report that satisfies ok.
func next(t *testing.T, out <-chan tea.Msg, ok func(cluster) bool) cluster {
	t.Helper()
	timeout := time.After(5 * time.Second)
	var last cluster
	for {
		select {
		case msg := <-out:
			last = msg.(liveMsg).c
			if ok(last) {
				return last
			}
		case <-timeout:
			t.Fatalf("no matching report; last %+v", last)
		}
	}
}

func TestWatch(t *testing.T) {
	c := fakeClients([]runtime.Object{node("lab-control-plane", true), node("lab-worker", false)},
		application("loki", "obs"), application("cert-manager", "core"), route("grafana.localtest.me"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan tea.Msg, 1)
	go watch(ctx, c, 1, out, time.Millisecond)

	got := next(t, out, func(c cluster) bool { return len(c.apps) == 2 && len(c.hosts) == 1 })
	if !got.up || got.nodes != 2 || got.nodesReady != 1 {
		t.Errorf("report %+v, want up with 1/2 nodes Ready", got)
	}
	if got.apps[0].Name != "cert-manager" || !got.apps[0].Ready() || got.hosts[0] != "grafana.localtest.me" {
		t.Errorf("apps %+v hosts %v", got.apps, got.hosts)
	}

	// A change arrives through the watch, not a new list.
	if _, err := c.Core.CoreV1().Nodes().UpdateStatus(ctx, node("lab-worker", true), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	next(t, out, func(c cluster) bool { return c.nodesReady == 2 })
}

// A cluster that does not answer is reported as off, not as an error.
func TestWatchClusterDown(t *testing.T) {
	c := fakeClients(nil)
	c.Core.(*fake.Clientset).PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("dial tcp 127.0.0.1:6443: connect: connection refused")
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan tea.Msg, 1)
	go watch(ctx, c, 1, out, time.Millisecond)
	got := next(t, out, func(c cluster) bool { return c.known })
	if got.up || got.reason == "" {
		t.Errorf("report %+v, want down with a reason", got)
	}
}
