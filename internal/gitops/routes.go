package gitops

import (
	"context"
	"io"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// The shared Gateway created by the core stack.
const (
	GatewayName      = "lab"
	GatewayNamespace = "envoy-gateway-system"
)

// httpRouteGVR is the Gateway API HTTPRoute resource.
var httpRouteGVR = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}

// AppHosts returns the sorted hostnames of HTTPRoutes attached to the lab
// Gateway, in every namespace. How a host becomes a URL is up to the mode.
func (r *Runner) AppHosts(ctx context.Context) ([]string, error) {
	c, err := r.clients(io.Discard)
	if err != nil {
		return nil, err
	}
	list, err := c.Dynamic.Resource(httpRouteGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var hosts []string
	for _, route := range list.Items {
		if !attachedToLab(route) {
			continue
		}
		names, _, _ := unstructured.NestedStringSlice(route.Object, "spec", "hostnames")
		hosts = append(hosts, names...)
	}
	slices.Sort(hosts)
	return slices.Compact(hosts), nil
}

// attachedToLab reports whether a parentRef names the lab Gateway. A parentRef
// without a namespace means the route's own namespace.
func attachedToLab(route unstructured.Unstructured) bool {
	refs, _, _ := unstructured.NestedSlice(route.Object, "spec", "parentRefs")
	for _, ref := range refs {
		m, _ := ref.(map[string]any)
		ns, _ := m["namespace"].(string)
		if ns == "" {
			ns = route.GetNamespace()
		}
		kind, _ := m["kind"].(string)
		if m["name"] == GatewayName && ns == GatewayNamespace && (kind == "" || kind == "Gateway") {
			return true
		}
	}
	return false
}
