package kube

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// WaitNodesReady watches nodes until at least want exist and every one is
// Ready. Progress lines go to log.
func WaitNodesReady(ctx context.Context, cs kubernetes.Interface, want int, log io.Writer) error {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
			return cs.CoreV1().Nodes().List(ctx, o)
		},
		WatchFuncWithContext: func(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
			return cs.CoreV1().Nodes().Watch(ctx, o)
		},
	}
	// The wrapper lets fake clients opt out of WatchList; real clients keep it.
	return waitStore(ctx, cache.ToListWatcherWithWatchListSemantics(lw, cs), &corev1.Node{}, func(items []any) (bool, string) {
		return nodesReady(items, want)
	}, log)
}

// nodesReady reports whether want or more nodes exist and all are Ready.
func nodesReady(items []any, want int) (bool, string) {
	var notReady []string
	for _, it := range items {
		n := it.(*corev1.Node)
		if !NodeReady(n) {
			notReady = append(notReady, n.Name)
		}
	}
	slices.Sort(notReady)
	ready := len(items) - len(notReady)
	status := fmt.Sprintf("nodes ready: %d/%d", ready, max(want, len(items)))
	if len(notReady) > 0 {
		status += " (waiting for " + strings.Join(notReady, ", ") + ")"
	}
	return len(items) >= want && len(notReady) == 0, status
}

// NodeReady reports whether the node's Ready condition is True.
func NodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}
