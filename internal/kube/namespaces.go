package kube

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// WaitNamespaceGone watches until the namespace no longer exists, so a
// following install does not hit a Terminating namespace.
func WaitNamespaceGone(ctx context.Context, cs kubernetes.Interface, name string, log io.Writer) error {
	sel := fields.OneTermEqualSelector("metadata.name", name).String()
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
			o.FieldSelector = sel
			return cs.CoreV1().Namespaces().List(ctx, o)
		},
		WatchFuncWithContext: func(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
			o.FieldSelector = sel
			return cs.CoreV1().Namespaces().Watch(ctx, o)
		},
	}
	return waitStore(ctx, cache.ToListWatcherWithWatchListSemantics(lw, cs), &corev1.Namespace{}, func(items []any) (bool, string) {
		for _, it := range items {
			if it.(*corev1.Namespace).Name == name {
				return false, "namespace " + name + " terminating"
			}
		}
		return true, "namespace " + name + " gone"
	}, log)
}
