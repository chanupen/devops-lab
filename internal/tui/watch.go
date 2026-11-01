package tui

import (
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/chanupen/devops-lab/internal/gitops"
	"github.com/chanupen/devops-lab/internal/kube"
)

// redrawEvery caps live updates at 20 a second.
const redrawEvery = 50 * time.Millisecond

// liveMsg carries a cluster report from the watcher started for gen.
type liveMsg struct {
	gen int
	c   cluster
}

// connect builds clients for a kubeconfig. Building them does not call the
// API server; the informers do that in the background.
func connect(path string) (*kube.Clients, error) {
	cfg, err := kube.RESTConfig(path)
	if err != nil {
		return nil, err
	}
	return kube.NewClients(kube.WarningsTo(cfg, io.Discard))
}

// watch runs informers for nodes, Argo CD Applications and HTTPRoutes until
// ctx ends. After each burst of changes it sends one liveMsg to out, at most
// once per every. The node informer decides whether the cluster is up: its
// list or watch failing means down, and any node event means up again.
// Applications and HTTPRoutes may not exist yet (before gitops or core), so
// their errors are ignored.
func watch(ctx context.Context, c *kube.Clients, gen int, out chan<- tea.Msg, every time.Duration) {
	changed := make(chan struct{}, 1)
	notify := func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	var mu sync.Mutex
	var down error
	setDown := func(err error) {
		mu.Lock()
		down = err
		mu.Unlock()
		notify()
	}

	nodes := informers.NewSharedInformerFactory(c.Core, 0).Core().V1().Nodes().Informer()
	_ = nodes.SetWatchErrorHandlerWithContext(func(_ context.Context, _ *cache.Reflector, err error) { setDown(err) })
	_, _ = nodes.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { setDown(nil) },
		UpdateFunc: func(any, any) { setDown(nil) },
		DeleteFunc: func(any) { setDown(nil) },
	})
	ignore := func(context.Context, *cache.Reflector, error) {}
	onChange := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { notify() },
		UpdateFunc: func(any, any) { notify() },
		DeleteFunc: func(any) { notify() },
	}
	apps := dynamicinformer.NewFilteredDynamicInformer(c.Dynamic, kube.AppGVR, gitops.Namespace, 0, cache.Indexers{}, nil).Informer()
	routes := dynamicinformer.NewFilteredDynamicInformer(c.Dynamic, gitops.HTTPRouteGVR, "", 0, cache.Indexers{}, nil).Informer()
	for _, inf := range []cache.SharedIndexInformer{apps, routes} {
		_ = inf.SetWatchErrorHandlerWithContext(ignore)
		_, _ = inf.AddEventHandler(onChange)
	}
	for _, inf := range []cache.SharedIndexInformer{nodes, apps, routes} {
		go inf.RunWithContext(ctx)
	}
	go func() {
		if cache.WaitForCacheSync(ctx.Done(), nodes.HasSynced) {
			notify()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
		mu.Lock()
		err := down
		mu.Unlock()
		var cl cluster
		switch {
		case err != nil:
			cl = cluster{known: true, reason: "not reachable: " + firstLine(err.Error())}
		case !nodes.HasSynced():
			continue
		default:
			cl = snapshot(nodes.GetStore().List(), apps.GetStore().List(), routes.GetStore().List())
		}
		select {
		case out <- liveMsg{gen: gen, c: cl}:
		case <-ctx.Done():
			return
		}
		select {
		case <-time.After(every):
		case <-ctx.Done():
			return
		}
	}
}

// snapshot builds a cluster report from the informer stores.
func snapshot(nodes, apps, routes []any) cluster {
	c := cluster{known: true, up: true, nodes: len(nodes)}
	for _, n := range nodes {
		if kube.NodeReady(n.(*corev1.Node)) {
			c.nodesReady++
		}
	}
	for _, a := range apps {
		c.apps = append(c.apps, kube.AppFrom(a.(*unstructured.Unstructured)))
	}
	slices.SortFunc(c.apps, func(a, b kube.App) int { return strings.Compare(a.Name, b.Name) })
	items := make([]unstructured.Unstructured, 0, len(routes))
	for _, r := range routes {
		items = append(items, *r.(*unstructured.Unstructured))
	}
	c.hosts = gitops.RouteHosts(items)
	return c
}
