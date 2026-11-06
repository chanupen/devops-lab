package gitops

import (
	"reflect"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

func TestAssistToolRBAC(t *testing.T) {
	tests := []struct {
		kind, name, namespace string
		rules                 []rbacv1.PolicyRule
	}{
		{"ClusterRole", "lab-assist-pods-events", "", []rbacv1.PolicyRule{{
			APIGroups: []string{""}, Resources: []string{"pods", "events"}, Verbs: []string{"get", "list"},
		}}},
		{"Role", "lab-assist-history", "argocd", []rbacv1.PolicyRule{{
			APIGroups: []string{"argoproj.io"}, Resources: []string{"applications"}, Verbs: []string{"get"},
		}}},
		{"Role", "lab-assist-query", "monitoring", []rbacv1.PolicyRule{{
			APIGroups: []string{""}, Resources: []string{"services/proxy"},
			ResourceNames: []string{"loki:3100", "kps-prometheus:9090"}, Verbs: []string{"get"},
		}}},
	}

	for _, mode := range []string{"local", "cloud"} {
		t.Run(mode, func(t *testing.T) {
			objects := renderMode(t, mode)
			find := func(kind, name string) map[string]any {
				t.Helper()
				for _, o := range objects {
					if o.App == "ollama" && o.Obj["kind"] == kind && field(o.Obj, "metadata", "name") == name {
						return o.Obj
					}
				}
				t.Fatalf("missing %s/%s", kind, name)
				return nil
			}
			var sa corev1.ServiceAccount
			if err := yaml.Unmarshal([]byte(toYAML(find("ServiceAccount", "assist-tools"))), &sa); err != nil {
				t.Fatal(err)
			}
			if sa.Namespace != "ai" || sa.AutomountServiceAccountToken == nil || *sa.AutomountServiceAccountToken {
				t.Fatalf("service account = %+v", sa)
			}
			bindings := map[string]bool{}
			for _, o := range objects {
				if o.Obj["kind"] != "RoleBinding" && o.Obj["kind"] != "ClusterRoleBinding" {
					continue
				}
				var b rbacv1.RoleBinding
				if err := yaml.Unmarshal([]byte(o.YAML), &b); err != nil {
					t.Fatal(err)
				}
				for _, subject := range b.Subjects {
					if subject.Kind == "ServiceAccount" && subject.Name == "assist-tools" && subject.Namespace == "ai" {
						bindings[b.Kind+"/"+b.Name] = true
					}
				}
			}
			wantBindings := map[string]bool{
				"ClusterRoleBinding/lab-assist-pods-events": true,
				"RoleBinding/lab-assist-history":            true,
				"RoleBinding/lab-assist-query":              true,
			}
			if !reflect.DeepEqual(bindings, wantBindings) {
				t.Fatalf("tool identity bindings = %v, want %v", bindings, wantBindings)
			}
			authCases := []struct {
				namespace, group, resource, name, verb string
				allowed                                bool
			}{
				{"demo", "", "pods", "example", "get", true},
				{"demo", "", "events", "", "list", true},
				{"demo", "", "pods", "example", "delete", false},
				{"demo", "", "pods/exec", "example", "create", false},
				{"demo", "", "secrets", "example", "get", false},
				{"argocd", "argoproj.io", "applications", "example", "get", true},
				{"demo", "argoproj.io", "applications", "example", "get", false},
				{"monitoring", "", "services/proxy", "loki:3100", "get", true},
				{"monitoring", "", "services/proxy", "kps-prometheus:9090", "get", true},
				{"monitoring", "", "services/proxy", "other:80", "get", false},
				{"monitoring", "", "services/proxy", "loki:3100", "create", false},
			}
			for _, a := range authCases {
				allowed := false
				for _, role := range tests {
					if role.namespace != "" && role.namespace != a.namespace {
						continue
					}
					for _, rule := range role.rules {
						if slices.Contains(rule.APIGroups, a.group) && slices.Contains(rule.Resources, a.resource) &&
							slices.Contains(rule.Verbs, a.verb) && (len(rule.ResourceNames) == 0 || slices.Contains(rule.ResourceNames, a.name)) {
							allowed = true
						}
					}
				}
				if allowed != a.allowed {
					t.Errorf("authorization for %+v = %v", a, allowed)
				}
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					var role rbacv1.Role
					if err := yaml.Unmarshal([]byte(toYAML(find(tt.kind, tt.name))), &role); err != nil {
						t.Fatal(err)
					}
					if role.Namespace != tt.namespace || !reflect.DeepEqual(role.Rules, tt.rules) {
						t.Fatalf("role namespace/rules = %q %+v, want %q %+v", role.Namespace, role.Rules, tt.namespace, tt.rules)
					}
					bindingKind := "RoleBinding"
					if tt.kind == "ClusterRole" {
						bindingKind = "ClusterRoleBinding"
					}
					var binding rbacv1.RoleBinding
					if err := yaml.Unmarshal([]byte(toYAML(find(bindingKind, tt.name))), &binding); err != nil {
						t.Fatal(err)
					}
					wantRef := rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: tt.kind, Name: tt.name}
					wantSubjects := []rbacv1.Subject{{Kind: "ServiceAccount", Name: "assist-tools", Namespace: "ai"}}
					if binding.Namespace != tt.namespace || binding.RoleRef != wantRef || !reflect.DeepEqual(binding.Subjects, wantSubjects) {
						t.Fatalf("binding = %+v", binding)
					}
				})
			}
		})
	}
}
