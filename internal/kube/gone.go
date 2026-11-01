package kube

import (
	"context"
	"fmt"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

// WaitAllGone watches a resource in every namespace (or a cluster-scoped
// one) until none is left, for example Karpenter's NodeClaims after the
// NodePools are deleted. Status lines go to log.
func WaitAllGone(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, log io.Writer) error {
	res := dyn.Resource(gvr)
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
			return res.List(ctx, o)
		},
		WatchFuncWithContext: func(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
			return res.Watch(ctx, o)
		},
	}
	return waitStore(ctx, cache.ToListWatcherWithWatchListSemantics(lw, dyn), &unstructured.Unstructured{}, func(items []any) (bool, string) {
		if len(items) == 0 {
			return true, fmt.Sprintf("no %s left", gvr.Resource)
		}
		return false, fmt.Sprintf("%d %s still terminating", len(items), gvr.Resource)
	}, log)
}
