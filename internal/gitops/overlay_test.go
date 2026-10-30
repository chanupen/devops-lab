package gitops

import (
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// The local overlay must keep Envoy on the NodePorts the kind control plane
// publishes (30080/30443 -> 127.0.0.1:8080/8443).
func TestLocalGatewayOverlay(t *testing.T) {
	dir := "../../gitops/overlays/local/core/gateway/"
	b, err := os.ReadFile(dir + "envoyproxy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var ep struct {
		Spec struct {
			Provider struct {
				Kubernetes struct {
					EnvoyService struct {
						Type  string `json:"type"`
						Patch struct {
							Value struct {
								Spec struct {
									Ports []struct {
										Port     int `json:"port"`
										NodePort int `json:"nodePort"`
									} `json:"ports"`
								} `json:"spec"`
							} `json:"value"`
						} `json:"patch"`
					} `json:"envoyService"`
				} `json:"kubernetes"`
			} `json:"provider"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(b, &ep); err != nil {
		t.Fatal(err)
	}
	svc := ep.Spec.Provider.Kubernetes.EnvoyService
	got := map[int]int{}
	for _, p := range svc.Patch.Value.Spec.Ports {
		got[p.Port] = p.NodePort
	}
	if svc.Type != "NodePort" || got[80] != 30080 || got[443] != 30443 {
		t.Errorf("envoy service = %s %v, want NodePort 80->30080, 443->30443", svc.Type, got)
	}
	k, err := os.ReadFile(dir + "kustomization.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"../../../../stacks/core/gateway", "envoyproxy.yaml", "name: lab-nodeport"} {
		if !strings.Contains(string(k), want) {
			t.Errorf("kustomization lacks %q", want)
		}
	}
}
