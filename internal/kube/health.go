package kube

import (
	"context"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Health is a one-time look at a cluster: its nodes and Argo CD apps.
type Health struct {
	Nodes, Ready int
	// Apps are the Applications in the Argo CD namespace, sorted by name;
	// nil when Argo CD is not installed.
	Apps []App
}

// ReadHealth lists nodes and the Applications in ns once. It is for a
// command that prints and exits (lab status); screens watch instead.
func ReadHealth(ctx context.Context, c *Clients, ns string) (Health, error) {
	nodes, err := c.Core.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return Health{}, fmt.Errorf("list nodes: %w", err)
	}
	h := Health{Nodes: len(nodes.Items)}
	for i := range nodes.Items {
		if NodeReady(&nodes.Items[i]) {
			h.Ready++
		}
	}
	apps, err := c.Dynamic.Resource(AppGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return h, nil // no Application CRD: Argo CD is not installed yet
	case err != nil:
		return h, fmt.Errorf("list Argo CD apps: %w", err)
	}
	h.Apps = []App{}
	for i := range apps.Items {
		h.Apps = append(h.Apps, AppFrom(&apps.Items[i]))
	}
	slices.SortFunc(h.Apps, func(a, b App) int { return strings.Compare(a.Name, b.Name) })
	return h, nil
}
