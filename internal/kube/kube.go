// Package kube holds client-go helpers: clients from a kubeconfig, and waits
// that watch resources through informers instead of polling.
package kube

import (
	"context"
	"fmt"
	"io"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

// Clients bundles the typed and dynamic clients for one cluster.
type Clients struct {
	Core    kubernetes.Interface
	Dynamic dynamic.Interface
}

// RESTConfig loads the current context of the kubeconfig at path.
func RESTConfig(path string) (*rest.Config, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, fmt.Errorf("kubeconfig %s: %w", path, err)
	}
	return cfg, nil
}

// WarningsTo sends API server warnings (deprecations, finalizer naming) to w
// instead of client-go's default stderr logger.
func WarningsTo(cfg *rest.Config, w io.Writer) *rest.Config {
	cfg.WarningHandler = rest.NewWarningWriter(w, rest.WarningWriterOptions{Deduplicate: true})
	return cfg
}

// NewClients builds clients for cfg.
func NewClients(cfg *rest.Config) (*Clients, error) {
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Clients{Core: core, Dynamic: dyn}, nil
}

// waitStore runs an informer over lw and calls check with every object in its
// store, after the initial list and after each change, until check reports
// done or ctx ends. Each new status line is written to log.
func waitStore(ctx context.Context, lw cache.ListerWatcher, obj runtime.Object,
	check func(items []any) (done bool, status string), log io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	changed := make(chan struct{}, 1)
	notify := func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	store, ctrl := cache.NewInformerWithOptions(cache.InformerOptions{
		ListerWatcher: lw,
		ObjectType:    obj,
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc:    func(any) { notify() },
			UpdateFunc: func(any, any) { notify() },
			DeleteFunc: func(any) { notify() },
		},
	})
	go ctrl.RunWithContext(ctx)
	if !cache.WaitForCacheSync(ctx.Done(), ctrl.HasSynced) {
		return fmt.Errorf("watch did not sync: %w", ctx.Err())
	}
	last := ""
	for {
		done, status := check(store.List())
		if status != last {
			_, _ = fmt.Fprintln(log, status)
			last = status
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", status, ctx.Err())
		case <-changed:
		}
	}
}
