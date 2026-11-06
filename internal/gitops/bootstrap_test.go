package gitops

import (
	"os"
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
	return render(t, stacks, "")
}

// render renders the bootstrap chart; mirror sets charts.ociMirror.
func render(t *testing.T, stacks map[string]any, mirror string) map[string]map[string]any {
	t.Helper()
	ch, err := loader.Load("../../gitops/bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]any{
		"mode":   "local",
		"repo":   map[string]any{"url": "git@github.com:o/r.git", "revision": "feat/x"},
		"stacks": stacks,
		"charts": map[string]any{"ociMirror": mirror},
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

// With every stack off only the OCI repository registrations remain.
func TestBootstrapChartCoreOff(t *testing.T) {
	for name := range renderBootstrap(t, false) {
		if !strings.HasPrefix(name, "Secret/oci-") {
			t.Errorf("core off renders %s", name)
		}
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
			map[string]string{"repoURL": "quay.io/jetstack/charts", "chart": "cert-manager", "targetRevision": "v1.21.2"},
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
	if s := objs["Secret/oci-docker-io-envoyproxy"]; field(s, "stringData", "enableOCI") != "true" || field(s, "stringData", "url") != "docker.io/envoyproxy" ||
		field(s, "stringData", "insecureOCIForceHttp") != nil {
		t.Errorf("OCI repo secret without a mirror = %v", s)
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
		app, repo, version, release, wave string
	}{
		{"kube-prometheus-stack", "ghcr.io/prometheus-community/charts", "92.10.1", "kps", "0"},
		{"loki", "ghcr.io/grafana/helm-charts", "7.3.0", "loki", "0"},
		{"otel-collector", "ghcr.io/open-telemetry/opentelemetry-helm-charts", "0.176.0", "otel-collector", "1"},
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
			wantChart := map[string]string{"otel-collector": "opentelemetry-collector"}[tt.app]
			if wantChart == "" {
				wantChart = tt.app
			}
			if chart["chart"] != wantChart || chart["repoURL"] != tt.repo || chart["targetRevision"] != tt.version || field(chart, "helm", "releaseName") != tt.release {
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
					field(o, "spec", "source", "repoURL") != "ghcr.io/stefanprodan/charts" ||
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
	for name := range renderStacks(t, map[string]any{"core": false, "obs": false, "policy": false, "sample-app": false}) {
		if !strings.HasPrefix(name, "Secret/oci-") {
			t.Errorf("all off renders %s", name)
		}
	}
}

// The local overlay's mirror routes every OCI chart through Zot.
func TestBootstrapChartLocalMirror(t *testing.T) {
	b, err := os.ReadFile("../../gitops/overlays/local/bootstrap/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var overlay struct {
		Charts struct {
			OCIMirror string `json:"ociMirror"`
		} `json:"charts"`
	}
	if err := yaml.Unmarshal(b, &overlay); err != nil || overlay.Charts.OCIMirror != "lab-zot:5000" {
		t.Fatalf("local overlay mirror = %q, %v", overlay.Charts.OCIMirror, err)
	}
	objs := render(t, map[string]any{"core": true, "obs": true, "policy": true, "sample-app": true}, overlay.Charts.OCIMirror)
	single := map[string]string{
		"cert-manager":     "lab-zot:5000/quay.io/jetstack/charts",
		"envoy-gateway":    "lab-zot:5000/docker.io/envoyproxy",
		"podinfo-frontend": "lab-zot:5000/ghcr.io/stefanprodan/charts",
		"metrics-server":   "https://kubernetes-sigs.github.io/metrics-server/", // no OCI chart
	}
	for app, want := range single {
		if got := field(objs["Application/"+app], "spec", "source", "repoURL"); got != want {
			t.Errorf("%s repoURL = %v, want %s", app, got, want)
		}
	}
	multi := map[string]string{
		"kube-prometheus-stack": "lab-zot:5000/ghcr.io/prometheus-community/charts",
		"loki":                  "lab-zot:5000/ghcr.io/grafana/helm-charts",
		"otel-collector":        "lab-zot:5000/ghcr.io/open-telemetry/opentelemetry-helm-charts",
		"kyverno":               "lab-zot:5000/ghcr.io/kyverno/charts",
		"kyverno-policies":      "lab-zot:5000/ghcr.io/kyverno/charts",
	}
	for app, want := range multi {
		srcs, _ := field(objs["Application/"+app], "spec", "sources").([]any)
		if len(srcs) == 0 || srcs[0].(map[string]any)["repoURL"] != want {
			t.Errorf("%s chart repoURL = %v, want %s", app, srcs, want)
		}
	}
	n := 0
	for name, o := range objs {
		if !strings.HasPrefix(name, "Secret/oci-") {
			continue
		}
		n++
		url, _ := field(o, "stringData", "url").(string)
		if !strings.HasPrefix(url, "lab-zot:5000/") || field(o, "stringData", "insecureOCIForceHttp") != "true" ||
			field(o, "stringData", "enableOCI") != "true" || field(o, "metadata", "labels", "argocd.argoproj.io/secret-type") != "repository" {
			t.Errorf("%s = %v", name, o["stringData"])
		}
	}
	if n != 9 {
		t.Errorf("%d OCI repository secrets, want 9", n)
	}
}

// policy: Kyverno, then the baseline Pod Security policies, each from the
// pinned chart with base values and the mode's overlay, server-side applied.
func TestBootstrapChartPolicy(t *testing.T) {
	objs := renderStacks(t, map[string]any{"policy": true})
	tests := []struct {
		app, wave string
	}{{"kyverno", "0"}, {"kyverno-policies", "1"}}
	for _, tt := range tests {
		o, ok := objs["Application/"+tt.app]
		if !ok {
			t.Fatalf("%s not rendered", tt.app)
		}
		srcs, _ := field(o, "spec", "sources").([]any)
		if len(srcs) != 2 {
			t.Fatalf("%s sources = %v", tt.app, srcs)
		}
		chart := srcs[0].(map[string]any)
		vf, _ := field(chart, "helm", "valueFiles").([]any)
		if chart["chart"] != tt.app || chart["targetRevision"] != "3.9.2" || chart["repoURL"] != "ghcr.io/kyverno/charts" ||
			len(vf) != 2 || vf[0] != "$values/gitops/stacks/policy/"+tt.app+"/values.yaml" || vf[1] != "$values/gitops/overlays/local/policy/"+tt.app+"/values.yaml" {
			t.Errorf("%s chart source = %v", tt.app, chart)
		}
		if field(o, "spec", "destination", "namespace") != "kyverno" || field(o, "metadata", "labels", "lab.stack") != "policy" ||
			field(o, "metadata", "annotations", "argocd.argoproj.io/sync-wave") != tt.wave {
			t.Errorf("%s = %v", tt.app, o["metadata"])
		}
		opts, _ := field(o, "spec", "syncPolicy", "syncOptions").([]any)
		if !slices.Contains(opts, any("ServerSideApply=true")) {
			t.Errorf("%s syncOptions = %v", tt.app, opts)
		}
		// Only kyverno ships the CRDs with empty metadata maps.
		ign, _ := field(o, "spec", "ignoreDifferences").([]any)
		if (tt.app == "kyverno") != (len(ign) == 1) {
			t.Errorf("%s ignoreDifferences = %v", tt.app, ign)
		}
	}
	for name := range renderStacks(t, map[string]any{"policy": false}) {
		if strings.Contains(name, "kyverno") && !strings.HasPrefix(name, "Secret/") {
			t.Errorf("policy off renders %s", name)
		}
	}
}
