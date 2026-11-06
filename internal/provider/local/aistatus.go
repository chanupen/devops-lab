package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/chanupen/devops-lab/internal/kube"
)

// AIStatus is the ai stack as lab doctor sees it. Each Err says why the
// field next to it could not be read.
type AIStatus struct {
	// Address is the Ollama container's on the kind network; AddressErr
	// also covers a container published beyond loopback.
	Address, AddressErr string
	// Slice is where ollama.ai.svc points (its EndpointSlice).
	Slice, SliceErr string
	// Version and Models are Ollama's, read from inside the cluster: from
	// the control-plane node, through the Service's ClusterIP, so through
	// kube-proxy's rules and the kind network, not the host's loopback.
	Version      string
	Models       []string
	InClusterErr string
	// LangfuseVersion is from /api/public/health, the same way.
	LangfuseVersion, LangfuseErr string
	// AppsNotReady are the ai stack's Argo CD apps that are not Synced and
	// Healthy, as "name sync/health".
	AppsNotReady []string
	Apps         int
}

// HostAIStatus reads AIStatus on this host: Podman's socket and the lab
// cluster in kubeconfig.
func HostAIStatus(ctx context.Context, kubeconfig string) (AIStatus, error) {
	sock, err := PodmanSocket()
	if err != nil {
		return AIStatus{}, err
	}
	cfg, err := kube.RESTConfig(kubeconfig)
	if err != nil {
		return AIStatus{}, err
	}
	c, err := kube.NewClients(kube.WarningsTo(cfg, io.Discard))
	if err != nil {
		return AIStatus{}, err
	}
	return ReadAIStatus(ctx, NewPodman(sock), c.Core, c.Dynamic), nil
}

// ReadAIStatus reads every part it can and records why the rest failed.
func ReadAIStatus(ctx context.Context, rt Runtime, core kubernetes.Interface, dyn dynamic.Interface) AIStatus {
	var st AIStatus
	addr, err := ollamaAddress(ctx, rt)
	st.Address, st.AddressErr = addr, errText(err)
	slice, err := core.DiscoveryV1().EndpointSlices(AINamespace).Get(ctx, OllamaSlice, metav1.GetOptions{})
	if err != nil {
		st.SliceErr = err.Error()
	} else {
		st.Slice = SliceAddress(slice)
	}
	node := ClusterName + "-control-plane"
	get := func(svc string, port int, path string) (string, error) {
		s, err := core.CoreV1().Services(AINamespace).Get(ctx, svc, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		return rt.Exec(ctx, node, []string{"curl", "-sf", "-m", "5", fmt.Sprintf("http://%s:%d%s", s.Spec.ClusterIP, port, path)})
	}
	if out, err := get(OllamaService, OllamaPort, "/api/version"); err != nil {
		st.InClusterErr = err.Error()
	} else {
		var v struct{ Version string }
		_ = json.Unmarshal([]byte(out), &v)
		st.Version = v.Version
		if out, err := get(OllamaService, OllamaPort, "/api/tags"); err != nil {
			st.InClusterErr = err.Error()
		} else {
			st.Models = ModelNames([]byte(out))
		}
	}
	if out, err := get("langfuse-web", 3000, "/api/public/health"); err != nil {
		st.LangfuseErr = err.Error()
	} else {
		var h struct{ Status, Version string }
		_ = json.Unmarshal([]byte(out), &h)
		if h.Status != "OK" {
			st.LangfuseErr = "health: " + strings.TrimSpace(out)
		}
		st.LangfuseVersion = h.Version
	}
	apps, err := dyn.Resource(kube.AppGVR).Namespace("argocd").List(ctx, metav1.ListOptions{LabelSelector: "lab.stack=ai"})
	if err != nil {
		st.AppsNotReady = []string{"cannot list Argo CD apps: " + err.Error()}
		return st
	}
	for i := range apps.Items {
		a := kube.AppFrom(&apps.Items[i])
		st.Apps++
		if !a.Ready() {
			st.AppsNotReady = append(st.AppsNotReady, fmt.Sprintf("%s %s/%s", a.Name, a.Sync, a.Health))
		}
	}
	slices.Sort(st.AppsNotReady)
	return st
}

// ModelNames are the model names in an Ollama /api/tags answer.
func ModelNames(tags []byte) []string {
	var t struct {
		Models []struct{ Name string } `json:"models"`
	}
	if json.Unmarshal(tags, &t) != nil {
		return nil
	}
	var out []string
	for _, m := range t.Models {
		out = append(out, m.Name)
	}
	return out
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
