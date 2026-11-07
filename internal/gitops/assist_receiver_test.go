package gitops

import (
	"fmt"
	"strings"
	"testing"
)

func TestAssistantReceiverRender(t *testing.T) {
	local := renderMode(t, "local")
	find := func(kind, name string) map[string]any {
		for _, o := range local {
			if o.App == "assist-receiver" && o.Obj["kind"] == kind && field(o.Obj, "metadata", "name") == name {
				return o.Obj
			}
		}
		return nil
	}
	for _, tt := range []struct {
		kind, name string
		wants      []string
	}{
		{"Deployment", "assist-receiver", []string{"lab.devops/ollama: \"true\"", "serviceAccountName: assist-tools", "expirationSeconds: 600", "name: langfuse-credentials", "@sha256:"}},
		{"NetworkPolicy", "assist-alertmanager-only", []string{"kubernetes.io/metadata.name: monitoring", "app.kubernetes.io/name: alertmanager", "port: 8080"}},
		{"AlertmanagerConfig", "assist-receiver", []string{"http://assist-receiver.ai.svc:8080/webhook"}},
	} {
		obj := find(tt.kind, tt.name)
		if obj == nil {
			t.Fatalf("%s %s not rendered", tt.kind, tt.name)
		}
		for _, want := range tt.wants {
			if !strings.Contains(toYAML(obj), want) {
				t.Errorf("%s %s lacks %q", tt.kind, tt.name, want)
			}
		}
	}
	foundMatcher := false
	configs := map[string]string{}
	for _, o := range local {
		if o.Obj["kind"] == "Alertmanager" &&
			field(o.Obj, "spec", "alertmanagerConfigMatcherStrategy", "type") == "None" {
			foundMatcher = true
		}
		if o.Obj["kind"] == "AlertmanagerConfig" {
			configs[fmt.Sprint(field(o.Obj, "metadata", "namespace"))+"/"+fmt.Sprint(field(o.Obj, "metadata", "name"))] = toYAML(o.Obj)
		}
	}
	if !foundMatcher {
		t.Error("local Alertmanager does not route across namespaces")
	}
	// matcherStrategy belongs to Alertmanager, not to configurations. Keep
	// every rendered configuration byte-for-byte free of that global setting.
	for name, config := range configs {
		if strings.Contains(config, "matcherStrategy") {
			t.Errorf("AlertmanagerConfig %s unexpectedly changes matcher strategy", name)
		}
	}
	for _, o := range renderMode(t, "cloud") {
		if o.App == "assist-receiver" {
			t.Error("assistant receiver rendered in cloud mode")
		}
	}
}
