package compat

import (
	"slices"
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
		{"local", []string{"Argo CD", "Kyverno", "kind node images"}},
		{"cloud", []string{"Karpenter", "AWS Load Balancer Controller", "Argo CD", "Kyverno"}},
		{"other", nil},
	}
	for _, tt := range tests {
		if got := names(For(tt.mode)); !slices.Equal(got, tt.want) {
			t.Errorf("For(%s) = %v, want %v", tt.mode, got, tt.want)
		}
	}
	for _, c := range Table {
		if c.MinK8s == "" || c.Source == "" || c.AppVersion == "" || len(c.Modes) == 0 {
			t.Errorf("%s: row needs MinK8s, AppVersion, Source and Modes", c.Name)
		}
	}
}

// Both labs land on 1.36 today: Argo CD 3.5 is tested up to 1.36.
func TestSupported(t *testing.T) {
	vs := []string{"1.37", "1.33", "1.34", "1.36", "1.35"}
	tests := []struct {
		mode      string
		supported []string
		preferred string
	}{
		{"local", []string{"1.34", "1.35", "1.36"}, "1.36"},
		{"cloud", []string{"1.33", "1.34", "1.35", "1.36"}, "1.36"},
	}
	for _, tt := range tests {
		if got := Supported(vs, For(tt.mode)); !slices.Equal(got, tt.supported) {
			t.Errorf("%s: Supported = %v, want %v", tt.mode, got, tt.supported)
		}
		if got := Preferred(vs, For(tt.mode)); got != tt.preferred {
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
