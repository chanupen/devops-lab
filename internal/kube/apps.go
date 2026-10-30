package kube

import (
	"context"
	"io"
	"reflect"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

// AppGVR is the Argo CD Application resource.
var AppGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

// App is the part of an Argo CD Application's status the lab waits on.
type App struct {
	Name   string
	Labels map[string]string
	Sync   string // Synced, OutOfSync, Unknown, or "" before the first reconcile
	Health string // Healthy, Progressing, Degraded, Missing, ...
	// Current is true when Argo CD has compared the live state against the
	// app's current source, so a just-changed spec is not reported as Synced
	// on the strength of the previous comparison.
	Current bool
	// Problems are Argo CD error conditions (SyncError, ComparisonError, ...)
	// as "Type: message", so a wait can say why it is stuck.
	Problems []string
}

// Ready reports Synced, Healthy and Current.
func (a App) Ready() bool { return a.Sync == "Synced" && a.Health == "Healthy" && a.Current }

// AppFrom reads an Application object.
func AppFrom(u *unstructured.Unstructured) App {
	sync, _, _ := unstructured.NestedString(u.Object, "status", "sync", "status")
	health, _, _ := unstructured.NestedString(u.Object, "status", "health", "status")
	src, _, _ := unstructured.NestedMap(u.Object, "spec", "source")
	compared, ok, _ := unstructured.NestedMap(u.Object, "status", "sync", "comparedTo", "source")
	var problems []string
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range conds {
		m, _ := c.(map[string]any)
		typ, _ := m["type"].(string)
		msg, _ := m["message"].(string)
		if strings.HasSuffix(typ, "Error") {
			problems = append(problems, typ+": "+msg)
		}
	}
	return App{
		Name: u.GetName(), Labels: u.GetLabels(), Sync: sync, Health: health,
		Current: ok && reflect.DeepEqual(src, compared), Problems: problems,
	}
}

// WaitApps watches Applications in ns and calls check with all of them after
// each change until it reports done or ctx ends. Status lines go to log.
func WaitApps(ctx context.Context, dyn dynamic.Interface, ns string,
	check func(apps []App) (done bool, status string), log io.Writer) error {
	res := dyn.Resource(AppGVR).Namespace(ns)
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
			return res.List(ctx, o)
		},
		WatchFuncWithContext: func(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
			return res.Watch(ctx, o)
		},
	}
	return waitStore(ctx, cache.ToListWatcherWithWatchListSemantics(lw, dyn), &unstructured.Unstructured{}, func(items []any) (bool, string) {
		apps := make([]App, 0, len(items))
		for _, it := range items {
			apps = append(apps, AppFrom(it.(*unstructured.Unstructured)))
		}
		return check(apps)
	}, log)
}
