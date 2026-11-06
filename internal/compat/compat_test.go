package compat

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestFor(t *testing.T) {
	names := func(cs []Component) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Name)
		}
		return out
	}
	tests := []struct {
		mode string
		want []string
	}{
		{"local", []string{"Argo CD", "Kyverno", "External Secrets Operator", "Moto", "Langfuse", "ClickHouse operator", "Ollama", "kind node images"}},
		{"cloud", []string{"Karpenter", "AWS Load Balancer Controller", "AWS EBS CSI driver", "Argo CD", "Kyverno", "External Secrets Operator"}},
		{"other", nil},
	}
	for _, tt := range tests {
		if got := names(For(tt.mode)); !slices.Equal(got, tt.want) {
			t.Errorf("For(%s) = %v, want %v", tt.mode, got, tt.want)
		}
	}
	for _, c := range Table {
		if (c.MinK8s == "" && !c.OutsideCluster) || c.Source == "" || c.AppVersion == "" || len(c.Modes) == 0 {
			t.Errorf("%s: row needs MinK8s (unless it runs outside the cluster), AppVersion, Source and Modes", c.Name)
		}
	}
}

// Both labs land on 1.36 today: Argo CD 3.5 is tested up to 1.36.
func TestSupported(t *testing.T) {
	vs := []string{"1.37", "1.33", "1.34", "1.36", "1.35"}
	minimal := []string{"registry", "cluster", "gitops", "core", "sample-app"}
	withSecrets := append(append([]string{}, minimal...), "secrets", "sample-db")
	tests := []struct {
		mode      string
		active    []string
		supported []string
		preferred string
	}{
		{"local", minimal, []string{"1.34", "1.35", "1.36"}, "1.36"},
		{"cloud", minimal, []string{"1.33", "1.34", "1.35", "1.36"}, "1.36"},
		// ESO states one version, 1.36: with secrets on, only that.
		{"local", withSecrets, []string{"1.36"}, "1.36"},
		{"cloud", withSecrets, []string{"1.36"}, "1.36"},
	}
	for _, tt := range tests {
		comps := ForStacks(tt.mode, tt.active)
		if got := Supported(vs, comps); !slices.Equal(got, tt.supported) {
			t.Errorf("%s %v: Supported = %v, want %v", tt.mode, tt.active, got, tt.supported)
		}
		if got := Preferred(vs, comps); got != tt.preferred {
			t.Errorf("%s: Preferred = %s, want %s", tt.mode, got, tt.preferred)
		}
	}
}

func TestLessAndRange(t *testing.T) {
	tests := []struct {
		a, b string
		less bool
	}{{"1.9", "1.37", true}, {"1.37", "1.9", false}, {"1.36", "1.36", false}, {"1.36", "1.36.1", true}}
	for _, tt := range tests {
		if got := Less(tt.a, tt.b); got != tt.less {
			t.Errorf("Less(%s, %s) = %v", tt.a, tt.b, got)
		}
	}
	if r := (Component{MinK8s: "1.19"}).Range(); r != "1.19+" {
		t.Errorf("open range = %s", r)
	}
	if r := (Component{MinK8s: "1.33", MaxK8s: "1.36"}).Range(); r != "1.33–1.36" {
		t.Errorf("range = %s", r)
	}
}

// Kyverno's 1.36 comes from its CI, not a stated range: allowed, flagged.
func TestTestedNotStated(t *testing.T) {
	up := func(stacks ...string) func(string) bool {
		return func(s string) bool { return slices.Contains(stacks, s) }
	}
	names := func(cs []Component) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Name)
		}
		return out
	}
	tests := []struct {
		name     string
		v        string
		deployed func(string) bool
		want     []string
	}{
		{"1.36 with the policy stack", "1.36", up("cluster", "gitops", "policy"), []string{"Kyverno"}},
		{"1.36 without it", "1.36", up("cluster", "gitops"), nil},
		{"1.35 is stated", "1.35", up("cluster", "gitops", "policy"), nil},
		{"1.37 is not supported at all", "1.37", up("policy"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := names(TestedNotStated(tt.v, For("local"), tt.deployed)); !slices.Equal(got, tt.want) {
				t.Errorf("TestedNotStated(%s) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
	// Still allowed: the table's range includes 1.36.
	if bad := Unsupported("1.36", For("local")); len(bad) != 0 {
		t.Errorf("1.36 unsupported: %v", bad)
	}
}

// A row with a Stack counts only while the stack is active.
func TestForStacks(t *testing.T) {
	names := func(cs []Component) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Name)
		}
		return out
	}
	tests := []struct {
		mode   string
		active []string
		want   []string
	}{
		{"local", nil, []string{"Argo CD", "Kyverno", "External Secrets Operator", "Moto", "Langfuse", "ClickHouse operator", "Ollama", "kind node images"}},
		{"local", []string{"cluster", "gitops", "secrets", "obs", "ai"}, []string{"Argo CD", "External Secrets Operator", "Moto", "Langfuse",
			"ClickHouse operator", "Ollama", "kind node images"}},
		{"local", []string{"cluster", "gitops"}, []string{"Argo CD", "kind node images"}},
		{"local", []string{"cluster", "gitops", "policy"}, []string{"Argo CD", "Kyverno", "kind node images"}},
		{"local", []string{"cluster", "gitops", "secrets"}, []string{"Argo CD", "External Secrets Operator", "Moto", "kind node images"}},
		{"cloud", []string{"cluster"}, []string{"Karpenter", "AWS Load Balancer Controller", "AWS EBS CSI driver"}},
	}
	for _, tt := range tests {
		if got := names(ForStacks(tt.mode, tt.active)); !slices.Equal(got, tt.want) {
			t.Errorf("ForStacks(%s, %v) = %v, want %v", tt.mode, tt.active, got, tt.want)
		}
	}
	for _, c := range Table {
		if c.Stack == "" {
			t.Errorf("%s has no Stack", c.Name)
		}
	}
}

// The ai stack's rows carry the pins the bootstrap template installs.
func TestAIRowsMatchPins(t *testing.T) {
	b, err := os.ReadFile("../../gitops/bootstrap/templates/ai.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tmpl := string(b)
	quadlet, err := os.ReadFile("../../docs/ollama.container")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range Table {
		switch c.Name {
		case "Langfuse":
			if !strings.Contains(tmpl, "chart: langfuse\n      targetRevision: "+c.ChartVersion+"\n") {
				t.Errorf("ai.yaml does not pin the langfuse chart at %s", c.ChartVersion)
			}
		case "ClickHouse operator":
			if !strings.Contains(tmpl, "chart: clickhouse-operator-helm\n      targetRevision: "+c.ChartVersion+"\n") {
				t.Errorf("ai.yaml does not pin clickhouse-operator-helm at %s", c.ChartVersion)
			}
		case "Ollama":
			if !strings.Contains(string(quadlet), "Image=docker.io/ollama/ollama:"+c.AppVersion+"@"+OllamaImageDigest+"\n") {
				t.Errorf("reference Quadlet does not match the Ollama compatibility pin")
			}
		}
	}
}
