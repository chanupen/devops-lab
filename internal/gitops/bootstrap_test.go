package gitops

import (
	"slices"
	"strings"
	"testing"

	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/common/util"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/engine"
	"sigs.k8s.io/yaml"
)

// renderBootstrap renders the repo's gitops/bootstrap chart with the values
// the gitops stack sets, and returns the objects by kind/name.
func renderBootstrap(t *testing.T, core bool) map[string]map[string]any {
	return renderStacks(t, map[string]any{"core": core})
}

func renderStacks(t *testing.T, stacks map[string]any) map[string]map[string]any {
	t.Helper()
	ch, err := loader.Load("../../gitops/bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]any{
		"mode":   "local",
		"repo":   map[string]any{"url": "git@github.com:o/r.git", "revision": "feat/x"},
		"stacks": stacks,
	}
	rv, err := util.ToRenderValues(ch, vals, common.ReleaseOptions{Name: RootApp, Namespace: Namespace}, common.DefaultCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	files, err := engine.Render(ch, rv)
	if err != nil {
		t.Fatal(err)
	}
	objs := map[string]map[string]any{}
	for name, content := range files {
		if strings.HasSuffix(name, ".tpl") {
			continue
		}
		for _, doc := range strings.Split(content, "\n---") {
			var o map[string]any
			if err := yaml.Unmarshal([]byte(doc), &o); err != nil {
				t.Fatalf("%s: %v\n%s", name, err, doc)
			}
			if len(o) == 0 {
				continue
			}
			md := o["metadata"].(map[string]any)
			objs[o["kind"].(string)+"/"+md["name"].(string)] = o
		}
	}
	return objs
}

func field(o map[string]any, path ...string) any {
	var cur any = o
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

func TestBootstrapChartCoreOff(t *testing.T) {
	if objs := renderBootstrap(t, false); len(objs) != 0 {
		t.Errorf("core off renders %d objects, want none", len(objs))
	}
}

func TestBootstrapChartCoreOn(t *testing.T) {
	objs := renderBootstrap(t, true)
	tests := []struct {
		app, namespace, wave string
		source               map[string]string // spec.source field -> value
		syncOptions          []string
	}{
		{"metrics-server", "kube-system", "0",
			map[string]string{"repoURL": "https://kubernetes-sigs.github.io/metrics-server/", "chart": "metrics-server", "targetRevision": "3.14.0"}, nil},
		{"cert-manager", "cert-manager", "0",
			map[string]string{"repoURL": "https://charts.jetstack.io", "chart": "cert-manager", "targetRevision": "v1.21.2"},
			[]string{"CreateNamespace=true"}},
		{"envoy-gateway", "envoy-gateway-system", "0",
			map[string]string{"repoURL": "docker.io/envoyproxy", "chart": "gateway-helm", "targetRevision": "v1.9.2"},
			[]string{"CreateNamespace=true", "ServerSideApply=true"}},
		{"core-gateway", "envoy-gateway-system", "1",
			map[string]string{"repoURL": "git@github.com:o/r.git", "targetRevision": "feat/x", "path": "gitops/overlays/local/core/gateway"},
			[]string{"SkipDryRunOnMissingResource=true"}},
	}
	for _, tt := range tests {
		t.Run(tt.app, func(t *testing.T) {
			o, ok := objs["Application/"+tt.app]
			if !ok {
				t.Fatalf("Application %s not rendered; have %v", tt.app, keys(objs))
			}
			if got := field(o, "metadata", "labels", "lab.stack"); got != "core" {
				t.Errorf("lab.stack = %v", got)
			}
			if got := field(o, "metadata", "annotations", "argocd.argoproj.io/sync-wave"); got != tt.wave {
				t.Errorf("wave = %v, want %s", got, tt.wave)
			}
			if f, _ := field(o, "metadata", "finalizers").([]any); len(f) != 1 || f[0] != "resources-finalizer.argocd.argoproj.io" {
				t.Errorf("finalizers = %v", f)
			}
			if got := field(o, "spec", "destination", "namespace"); got != tt.namespace {
				t.Errorf("namespace = %v", got)
			}
			for k, v := range tt.source {
				if got := field(o, "spec", "source", k); got != v {
					t.Errorf("source.%s = %v, want %s", k, got, v)
				}
			}
			var opts []string
			if l, ok := field(o, "spec", "syncPolicy", "syncOptions").([]any); ok {
				for _, x := range l {
					opts = append(opts, x.(string))
				}
			}
			if !slices.Equal(opts, tt.syncOptions) {
				t.Errorf("syncOptions = %v, want %v", opts, tt.syncOptions)
			}
			if field(o, "spec", "syncPolicy", "automated", "prune") != true {
				t.Error("automated prune is off")
			}
		})
	}
	if args, _ := field(objs["Application/metrics-server"], "spec", "source", "helm", "valuesObject", "args").([]any); len(args) != 1 ||
		args[0] != "--kubelet-insecure-tls" {
		t.Errorf("metrics-server args = %v", args)
	}
	if s := objs["Secret/repo-envoyproxy-oci"]; field(s, "stringData", "enableOCI") != "true" || field(s, "stringData", "url") != "docker.io/envoyproxy" {
		t.Errorf("OCI repo secret = %v", s)
	}
}

func keys(m map[string]map[string]any) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	slices.Sort(k)
	return k
}

func TestBootstrapChartObs(t *testing.T) {
	objs := renderStacks(t, map[string]any{"core": true, "obs": true})
	tests := []struct {
		app, chart, version, release, wave string
	}{
		{"kube-prometheus-stack", "kube-prometheus-stack", "92.10.1", "kps", "0"},
		{"loki", "loki", "7.3.0", "loki", "0"},
		{"alloy", "alloy", "1.13.1", "alloy", "1"},
	}
	for _, tt := range tests {
		t.Run(tt.app, func(t *testing.T) {
			o, ok := objs["Application/"+tt.app]
			if !ok {
				t.Fatalf("not rendered; have %v", keys(objs))
			}
			if field(o, "metadata", "labels", "lab.stack") != "obs" || field(o, "metadata", "annotations", "argocd.argoproj.io/sync-wave") != tt.wave {
				t.Errorf("metadata = %v", o["metadata"])
			}
			srcs, _ := field(o, "spec", "sources").([]any)
			if len(srcs) != 2 {
				t.Fatalf("sources = %v", srcs)
			}
			chart, values := srcs[0].(map[string]any), srcs[1].(map[string]any)
			if chart["chart"] != tt.chart || chart["targetRevision"] != tt.version || field(chart, "helm", "releaseName") != tt.release {
				t.Errorf("chart source = %v", chart)
			}
			files, _ := field(chart, "helm", "valueFiles").([]any)
			want := []any{"$values/gitops/stacks/obs/" + tt.app + "/values.yaml", "$values/gitops/overlays/local/obs/" + tt.app + "/values.yaml"}
			if !slices.Equal(files, want) || field(chart, "helm", "ignoreMissingValueFiles") != true {
				t.Errorf("valueFiles = %v", files)
			}
			if values["ref"] != "values" || values["targetRevision"] != "feat/x" || values["repoURL"] != "git@github.com:o/r.git" {
				t.Errorf("values source = %v", values)
			}
			if field(o, "spec", "destination", "namespace") != "monitoring" {
				t.Errorf("namespace = %v", field(o, "spec", "destination", "namespace"))
			}
			opts, _ := field(o, "spec", "syncPolicy", "syncOptions").([]any)
			if !slices.Contains(opts, any("ServerSideApply=true")) {
				t.Errorf("syncOptions = %v", opts)
			}
		})
	}
	if got := field(objs["Application/obs-routes"], "spec", "source", "path"); got != "gitops/overlays/local/obs/routes" {
		t.Errorf("obs-routes path = %v", got)
	}
}

func TestBootstrapChartSampleApp(t *testing.T) {
	tests := []struct {
		name string
		obs  bool
	}{{"without obs", false}, {"with obs", true}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := renderStacks(t, map[string]any{"core": true, "obs": tt.obs, "sample-app": true})
			for _, app := range []string{"podinfo-backend", "podinfo-frontend"} {
				o, ok := objs["Application/"+app]
				if !ok {
					t.Fatalf("%s not rendered", app)
				}
				if field(o, "spec", "source", "chart") != "podinfo" || field(o, "spec", "source", "targetRevision") != "6.15.0" ||
					field(o, "spec", "source", "helm", "releaseName") != app || field(o, "spec", "destination", "namespace") != "podinfo" {
					t.Errorf("%s source = %v", app, field(o, "spec", "source"))
				}
				if got := field(o, "spec", "source", "helm", "valuesObject", "serviceMonitor", "enabled"); got != tt.obs {
					t.Errorf("%s serviceMonitor.enabled = %v, want %v", app, got, tt.obs)
				}
			}
			if got := field(objs["Application/podinfo-frontend"], "spec", "source", "helm", "valuesObject", "backend"); got != "http://podinfo-backend.podinfo:9898/echo" {
				t.Errorf("frontend backend = %v", got)
			}
			if got := field(objs["Application/podinfo-backend"], "spec", "source", "helm", "valuesObject", "backend"); got != nil {
				t.Errorf("backend should not call another backend, got %v", got)
			}
			if got := field(objs["Application/sample-app-routes"], "spec", "source", "path"); got != "gitops/overlays/local/sample-app/routes" {
				t.Errorf("routes path = %v", got)
			}
		})
	}
	if objs := renderStacks(t, map[string]any{"core": false, "obs": false, "sample-app": false}); len(objs) != 0 {
		t.Errorf("all off renders %v", keys(objs))
	}
}
