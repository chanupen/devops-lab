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

// Apps use *.localtest.me, which resolves to 127.0.0.1; the domain lives only
// in the local overlay, and the base Gateway names no host.
func TestLocalAppDomain(t *testing.T) {
	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile("../../gitops/" + p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	tests := []struct {
		file    string
		want    []string
		wantNot []string
	}{
		{"overlays/local/core/gateway/certificate.yaml", []string{"- localtest.me", `- "*.localtest.me"`, "secretName: lab-gateway-tls"}, nil},
		{"overlays/local/core/gateway/argocd-route.yaml",
			[]string{"- argocd.localtest.me", "name: lab", "namespace: envoy-gateway-system", "sectionName: https", "name: argocd-server"}, nil},
		{"overlays/local/core/gateway/kustomization.yaml", []string{"- certificate.yaml", "- argocd-route.yaml"}, nil},
		{"stacks/core/gateway/gateway.yaml", []string{"name: lab-gateway-tls"}, []string{"hostname:", "localtest.me", "lab.localhost"}},
		{"stacks/core/gateway/kustomization.yaml", nil, []string{"certificate.yaml"}},
	}
	for _, tt := range tests {
		s := read(tt.file)
		for _, w := range tt.want {
			if !strings.Contains(s, w) {
				t.Errorf("%s lacks %q", tt.file, w)
			}
		}
		for _, w := range tt.wantNot {
			if strings.Contains(s, w) {
				t.Errorf("%s should not contain %q", tt.file, w)
			}
		}
	}
}

// The obs and sample-app values and routes keep the settings the lab relies on.
func TestObsAndSampleAppFiles(t *testing.T) {
	tests := []struct {
		file    string
		want    []string
		wantNot []string
	}{
		{"stacks/obs/kube-prometheus-stack/values.yaml", []string{"retention: 24h", "fullnameOverride: kps",
			"alertmanager:\n  enabled: true", "nodeExporter:\n  enabled: true", "kubeStateMetrics:\n  enabled: true",
			"existingSecret: grafana-admin", "serviceMonitorSelectorNilUsesHelmValues: false", "url: http://loki.monitoring.svc:3100"},
			[]string{"adminPassword"}},
		{"overlays/local/obs/kube-prometheus-stack/values.yaml", []string{"kubeEtcd:\n  enabled: false", "kubeScheduler:\n  enabled: false",
			"kubeControllerManager:\n  enabled: false", "kubeProxy:\n  enabled: false"}, nil},
		{"stacks/obs/loki/values.yaml", []string{"deploymentMode: SingleBinary", "type: filesystem", "replication_factor: 1",
			"read: {replicas: 0}", "gateway: {enabled: false}"}, nil},
		// Without persistence the chart mounts nothing at /var/loki on a read-only root.
		{"overlays/local/obs/loki/values.yaml", []string{"persistence:\n    enabled: false", "mountPath: /var/loki", "emptyDir: {}"}, nil},
		{"stacks/obs/alloy/values.yaml", []string{`loki.source.kubernetes "pods"`, `url = "http://loki.monitoring.svc:3100/loki/api/v1/push"`}, nil},
		{"overlays/local/obs/routes/grafana-route.yaml", []string{"- grafana.localtest.me", "name: kps-grafana", "port: 80", "sectionName: https"}, nil},
		{"overlays/local/sample-app/routes/podinfo-route.yaml", []string{"- podinfo.localtest.me", "name: podinfo-frontend", "port: 9898", "sectionName: https"}, nil},
	}
	for _, tt := range tests {
		b, err := os.ReadFile("../../gitops/" + tt.file)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range tt.want {
			if !strings.Contains(string(b), w) {
				t.Errorf("%s lacks %q", tt.file, w)
			}
		}
		for _, w := range tt.wantNot {
			if strings.Contains(string(b), w) {
				t.Errorf("%s should not contain %q", tt.file, w)
			}
		}
	}
}
