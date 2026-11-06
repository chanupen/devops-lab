package gitops

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"helm.sh/helm/v4/pkg/action"
	ci "helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/cli"
	kubefake "helm.sh/helm/v4/pkg/kube/fake"
	ri "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"
)

// The render tests turn the app-of-apps into what Argo CD would apply, for
// each mode, with every stack on: the bootstrap chart with the mode's
// overlay values, then each Application it renders, as Argo CD renders it
// (helm template of the pinned chart with the base and overlay value
// files, or kustomize build of the in-repo path). Charts are fetched once
// into the user cache (devops-lab/charts) and reused, so later runs need
// no network.

const repoRoot = "../.."

// rendered is one object an Application deploys.
type rendered struct {
	App  string
	Obj  map[string]any
	YAML string
}

var renderOnce sync.Map // mode -> *renderResult

// discardHandler drops Helm's SDK log.
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (h discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return h }
func (h discardHandler) WithGroup(string) slog.Handler           { return h }

type renderResult struct {
	once sync.Once
	objs []rendered
	err  error
}

// renderMode renders every stack of mode, once per test binary.
func renderMode(t *testing.T, mode string) []rendered {
	t.Helper()
	v, _ := renderOnce.LoadOrStore(mode, &renderResult{})
	r := v.(*renderResult)
	r.once.Do(func() { r.objs, r.err = renderAll(mode) })
	if r.err != nil {
		t.Fatalf("render %s: %v", mode, r.err)
	}
	return r.objs
}

// sampleImage is what the sample-db stack sets per mode (by digest).
var sampleImage = map[string]string{
	"local": "lab-zot:5000/lab/sample-db@sha256:" + strings.Repeat("a", 64),
	"cloud": "123456789012.dkr.ecr.ap-south-1.amazonaws.com/lab/sample-db@sha256:" + strings.Repeat("a", 64),
}

func renderAll(mode string) ([]rendered, error) {
	apps, err := bootstrapApps(mode)
	if err != nil {
		return nil, err
	}
	var out []rendered
	for _, app := range apps {
		objs, err := renderApp(mode, app)
		if err != nil {
			return nil, fmt.Errorf("Application %s: %w", field(app, "metadata", "name"), err)
		}
		out = append(out, objs...)
	}
	return out, nil
}

// bootstrapApps renders the bootstrap chart as the gitops stack installs it
// (overlays/<mode>/bootstrap/values.yaml, every stack on) and returns its
// Applications.
func bootstrapApps(mode string) ([]map[string]any, error) {
	ch, err := loader.Load(filepath.Join(repoRoot, BootstrapPath))
	if err != nil {
		return nil, err
	}
	vals := map[string]any{}
	if err := mergeFile(vals, filepath.Join(repoRoot, "gitops/overlays", mode, "bootstrap/values.yaml")); err != nil {
		return nil, err
	}
	stacks := map[string]any{}
	for _, s := range appStacks {
		stacks[s] = true
	}
	merge(vals, map[string]any{
		"mode":     mode,
		"repo":     map[string]any{"url": "git@github.com:o/r.git", "revision": "main"},
		"stacks":   stacks,
		"sampleDB": map[string]any{"image": sampleImage[mode]},
	})
	manifest, err := helmTemplate(ch, RootApp, Namespace, vals)
	if err != nil {
		return nil, err
	}
	var apps []map[string]any
	for _, o := range splitYAML(manifest) {
		if o["kind"] == "Application" {
			apps = append(apps, o)
		}
	}
	if len(apps) == 0 {
		return nil, fmt.Errorf("the bootstrap chart rendered no Applications")
	}
	return apps, nil
}

// renderApp renders what one Application deploys.
func renderApp(mode string, app map[string]any) ([]rendered, error) {
	name, _ := field(app, "metadata", "name").(string)
	ns, _ := field(app, "spec", "destination", "namespace").(string)
	var sources []map[string]any
	if s, ok := field(app, "spec", "source").(map[string]any); ok {
		sources = append(sources, s)
	}
	if list, ok := field(app, "spec", "sources").([]any); ok {
		for _, s := range list {
			sources = append(sources, s.(map[string]any))
		}
	}
	var docs []map[string]any
	for _, src := range sources {
		var objs []map[string]any
		var err error
		switch {
		case src["ref"] != nil:
			continue // a values source ($values)
		case src["chart"] != nil:
			objs, err = renderRemoteChart(mode, src, ns)
		case src["path"] != nil:
			objs, err = renderPath(src, ns)
		default:
			err = fmt.Errorf("a source with neither chart, path nor ref: %v", src)
		}
		if err != nil {
			return nil, err
		}
		docs = append(docs, objs...)
	}
	out := make([]rendered, 0, len(docs))
	for _, o := range docs {
		b, _ := yaml.Marshal(o)
		out = append(out, rendered{App: name, Obj: o, YAML: string(b)})
	}
	return out, nil
}

// renderPath renders an in-repo source: kustomize build, or a local chart.
func renderPath(src map[string]any, ns string) ([]map[string]any, error) {
	dir := filepath.Join(repoRoot, src["path"].(string))
	if _, err := os.Stat(filepath.Join(dir, "Chart.yaml")); err == nil {
		ch, err := loader.Load(dir)
		if err != nil {
			return nil, err
		}
		vals, err := helmValues(src, func(f string) string { return filepath.Join(dir, f) })
		if err != nil {
			return nil, err
		}
		release, _ := field(src, "helm", "releaseName").(string)
		if release == "" {
			release = filepath.Base(dir)
		}
		m, err := helmTemplate(ch, release, ns, vals)
		if err != nil {
			return nil, err
		}
		return splitYAML(m), nil
	}
	k := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	res, err := k.Run(filesys.MakeFsOnDisk(), dir)
	if err != nil {
		return nil, err
	}
	b, err := res.AsYaml()
	if err != nil {
		return nil, err
	}
	return splitYAML(string(b)), nil
}

// renderRemoteChart renders a chart source: the pinned chart from its
// repository (in local mode without the Zot mirror prefix: the test fetches
// upstream), with the source's value files and inline values.
func renderRemoteChart(mode string, src map[string]any, ns string) ([]map[string]any, error) {
	repo, _ := src["repoURL"].(string)
	chart, _ := src["chart"].(string)
	version, _ := src["targetRevision"].(string)
	if mirror := mirrorOf(mode); mirror != "" {
		repo = strings.TrimPrefix(repo, mirror+"/")
	}
	r := HelmRelease{Name: chart, Version: version}
	if strings.HasPrefix(repo, "http://") || strings.HasPrefix(repo, "https://") {
		r.RepoURL, r.Chart = repo, chart
	} else {
		r.Chart = "oci://" + repo + "/" + chart
	}
	path, err := fetchChart(r)
	if err != nil {
		return nil, err
	}
	ch, err := loader.Load(path)
	if err != nil {
		return nil, err
	}
	vals, err := helmValues(src, func(f string) string {
		return filepath.Join(repoRoot, strings.TrimPrefix(f, "$values/"))
	})
	if err != nil {
		return nil, err
	}
	release, _ := field(src, "helm", "releaseName").(string)
	if release == "" {
		release = chart
	}
	m, err := helmTemplate(ch, release, ns, vals)
	if err != nil {
		return nil, err
	}
	return splitYAML(m), nil
}

func mirrorOf(mode string) string {
	vals := map[string]any{}
	if err := mergeFile(vals, filepath.Join(repoRoot, "gitops/overlays", mode, "bootstrap/values.yaml")); err != nil {
		return ""
	}
	m, _ := field(vals, "charts", "ociMirror").(string)
	return m
}

var fetchMu sync.Mutex

// fetchChart returns the chart archive from the user cache, downloading it
// the first time.
func fetchChart(r HelmRelease) (string, error) {
	fetchMu.Lock()
	defer fetchMu.Unlock()
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	home := filepath.Join(cache, "devops-lab", "render-test")
	settings := cli.New()
	settings.RepositoryConfig = filepath.Join(home, "repositories.yaml")
	settings.RepositoryCache = filepath.Join(home, "cache")
	settings.ContentCache = filepath.Join(home, "content")
	return (&sdkHelm{home: home}).chart(context.Background(), settings, r, io.Discard)
}

// helmValues are a source's values as Argo CD merges them: value files in
// order (missing ones skipped when ignoreMissingValueFiles), valuesObject,
// then parameters.
func helmValues(src map[string]any, resolve func(string) string) (map[string]any, error) {
	vals := map[string]any{}
	ignoreMissing, _ := field(src, "helm", "ignoreMissingValueFiles").(bool)
	files, _ := field(src, "helm", "valueFiles").([]any)
	for _, f := range files {
		p := resolve(f.(string))
		if err := mergeFile(vals, p); err != nil {
			if os.IsNotExist(err) && ignoreMissing {
				continue
			}
			return nil, err
		}
	}
	if obj, ok := field(src, "helm", "valuesObject").(map[string]any); ok {
		merge(vals, obj)
	}
	params, _ := field(src, "helm", "parameters").([]any)
	for _, p := range params {
		pm := p.(map[string]any)
		setPath(vals, pm["name"].(string), pm["value"])
	}
	return vals, nil
}

// setPath sets a Helm --set style dotted name (no list indexes needed here).
func setPath(vals map[string]any, name string, v any) {
	parts := strings.Split(name, ".")
	cur := vals
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = v
}

// renderAPIs are the API groups the lab's stacks provide, so charts render
// the objects they gate on them (ServiceMonitors, Gateway routes, ...).
var renderAPIs = common.VersionSet{
	"monitoring.coreos.com/v1", "monitoring.coreos.com/v1/ServiceMonitor", "monitoring.coreos.com/v1/PrometheusRule",
	"gateway.networking.k8s.io/v1", "cert-manager.io/v1", "external-secrets.io/v1", "kyverno.io/v1",
	"policyreport.wgpolicyk8s.io/v1alpha2", "argoproj.io/v1alpha1", "apiextensions.k8s.io/v1",
}

// helmTemplate is helm template: a client-only dry-run install with CRDs.
func helmTemplate(ch ci.Charter, release, ns string, vals map[string]any) (string, error) {
	cfg := action.NewConfiguration(action.ConfigurationSetLogger(discardHandler{}))
	cfg.Releases = storage.Init(driver.NewMemory())
	cfg.KubeClient = &kubefake.PrintingKubeClient{Out: io.Discard}
	cfg.Capabilities = common.DefaultCapabilities.Copy()
	i := action.NewInstall(cfg)
	i.DryRunStrategy = action.DryRunClient
	i.Replace, i.IncludeCRDs = true, true
	i.ReleaseName, i.Namespace = release, ns
	i.KubeVersion = &common.KubeVersion{Version: "v1.36.0", Major: "1", Minor: "36"}
	i.APIVersions = renderAPIs
	rel, err := i.Run(ch, vals)
	if err != nil {
		return "", err
	}
	r, ok := rel.(*ri.Release)
	if !ok {
		return "", fmt.Errorf("unexpected release type %T", rel)
	}
	var b strings.Builder
	b.WriteString(r.Manifest)
	for _, h := range r.Hooks {
		b.WriteString("\n---\n" + h.Manifest)
	}
	return b.String(), nil
}

func splitYAML(s string) []map[string]any {
	var out []map[string]any
	for _, doc := range regexp.MustCompile(`(?m)^---\s*$`).Split(s, -1) {
		var o map[string]any
		if err := yaml.Unmarshal([]byte(doc), &o); err != nil || len(o) == 0 {
			continue
		}
		out = append(out, o)
	}
	return out
}

func mergeFile(vals map[string]any, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var v map[string]any
	if err := yaml.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	merge(vals, v)
	return nil
}

// merge deep-merges src into dst (maps merge, everything else replaces).
func merge(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				merge(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

func kindName(o map[string]any) string {
	ns, _ := field(o, "metadata", "namespace").(string)
	name, _ := field(o, "metadata", "name").(string)
	return fmt.Sprintf("%v %s/%s", o["kind"], ns, name)
}

// Both modes render: every stack, every Application.
func TestRenderEveryStack(t *testing.T) {
	for _, mode := range overlayModes {
		t.Run(mode, func(t *testing.T) {
			objs := renderMode(t, mode)
			apps := map[string]int{}
			for _, o := range objs {
				apps[o.App]++
			}
			for _, want := range []string{"cert-manager", "envoy-gateway", "core-gateway", "kube-prometheus-stack", "loki",
				"otel-collector", "obs-routes", "external-secrets", "secrets-store", "sample-db", "kyverno", "podinfo-frontend"} {
				if apps[want] == 0 {
					t.Errorf("Application %s rendered nothing (rendered: %v)", want, apps)
				}
			}
		})
	}
}

// What only one mode may contain. Cloud: no load balancer, no Zot, no
// Moto; local: no ECR. The patterns match the names, not words that
// contain them ("promotion").
var modeForbidden = map[string][]*regexp.Regexp{
	"cloud": {regexp.MustCompile(`lab-zot`), regexp.MustCompile(`127\.0\.0\.1:5000`), regexp.MustCompile(`\blab-moto\b`),
		regexp.MustCompile(`moto-credentials`), regexp.MustCompile(`AWS_SECRETSMANAGER_ENDPOINT`), regexp.MustCompile(`:4566\b`),
		regexp.MustCompile(`(?i)\bnat[-_ ]?gateway\b`)},
	"local": {regexp.MustCompile(`\.dkr\.ecr\.`)},
}

func TestRenderModeDifferences(t *testing.T) {
	for _, mode := range overlayModes {
		t.Run(mode, func(t *testing.T) {
			for _, o := range renderMode(t, mode) {
				for _, re := range modeForbidden[mode] {
					if loc := re.FindStringIndex(o.YAML); loc != nil {
						t.Errorf("%s (%s) contains %q: %s", kindName(o.Obj), o.App, re, o.YAML[max(0, loc[0]-80):min(len(o.YAML), loc[1]+80)])
					}
				}
				if o.Obj["kind"] == "Service" && field(o.Obj, "spec", "type") == "LoadBalancer" {
					t.Errorf("%s (%s) is a LoadBalancer Service: no load balancer in either mode (lab tunnel, NodePorts)", kindName(o.Obj), o.App)
				}
				// Envoy Gateway makes Envoy's Service at run time, of the
				// type the EnvoyProxy names.
				if o.Obj["kind"] == "EnvoyProxy" {
					want := map[string]string{"local": "NodePort", "cloud": "ClusterIP"}[mode]
					if got := field(o.Obj, "spec", "provider", "kubernetes", "envoyService", "type"); got != want {
						t.Errorf("%s (%s): Envoy's Service is %v, want %s", kindName(o.Obj), o.App, got, want)
					}
				}
				if o.Obj["kind"] == "Ingress" {
					t.Errorf("%s (%s): no Ingress (the Gateway routes apps; an Ingress would make a load balancer in cloud mode)", kindName(o.Obj), o.App)
				}
			}
		})
	}
}

// Cloud mode creates AWS resources from the cluster in one way only: EBS
// volumes for claims on lab-gp3, whose driver tags them (extraVolumeTags,
// tested in internal/provider/aws). Every claim must use it, so none can
// end up untagged on another class.
func TestRenderCloudVolumesTagged(t *testing.T) {
	claims := 0
	for _, o := range renderMode(t, "cloud") {
		var specs []map[string]any
		switch o.Obj["kind"] {
		case "PersistentVolumeClaim":
			specs = append(specs, o.Obj["spec"].(map[string]any))
		case "StatefulSet":
			list, _ := field(o.Obj, "spec", "volumeClaimTemplates").([]any)
			for _, c := range list {
				specs = append(specs, c.(map[string]any)["spec"].(map[string]any))
			}
		case "Prometheus", "Alertmanager":
			if s, ok := field(o.Obj, "spec", "storage", "volumeClaimTemplate", "spec").(map[string]any); ok {
				specs = append(specs, s)
			}
		case "StorageClass":
			t.Errorf("%s (%s): StorageClasses come from lab (lab-gp3, tagged), not Git", kindName(o.Obj), o.App)
		}
		for _, s := range specs {
			claims++
			if s["storageClassName"] != "lab-gp3" {
				t.Errorf("%s (%s) claims storage on %v, want lab-gp3 (the tagged gp3 class)", kindName(o.Obj), o.App, s["storageClassName"])
			}
		}
	}
	// Postgres and Prometheus.
	if claims < 2 {
		t.Errorf("found %d volume claims in cloud mode, want Postgres's and Prometheus's", claims)
	}
}

// The cloud network has no NAT gateway, and every module tags its
// resources through default_tags.
func TestCloudTerraformNoNATAndTagged(t *testing.T) {
	err := filepath.WalkDir(filepath.Join(repoRoot, "infra/terraform/aws"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".tf") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if regexp.MustCompile(`resource\s+"aws_nat_gateway"`).Match(b) {
			t.Errorf("%s has a NAT gateway; the lab has none (public subnets, no NAT)", p)
		}
		if filepath.Base(p) == "versions.tf" && strings.Contains(string(b), `provider "aws"`) &&
			!strings.Contains(string(b), "default_tags") {
			t.Errorf("%s: the aws provider has no default_tags", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"cluster", "cluster/registry"} {
		b, err := os.ReadFile(filepath.Join(repoRoot, "infra/terraform/aws", dir, "main.tf"))
		if err != nil {
			t.Fatal(err)
		}
		for _, tag := range []string{`lab          = "true"`, `owner        = var.owner`, `"expires-at" = var.expires_at`} {
			if !strings.Contains(string(b), tag) {
				t.Errorf("%s/main.tf tags lack %s", dir, tag)
			}
		}
	}
}

// Overlay files that hold no mode difference stay identical in both modes.
func TestOverlaySharedFilesIdentical(t *testing.T) {
	for _, f := range []string{"core/gateway/certificate.yaml", "core/gateway/argocd-route.yaml",
		"obs/routes/grafana-route.yaml", "sample-app/routes/podinfo-route.yaml"} {
		a, errA := os.ReadFile(filepath.Join(repoRoot, "gitops/overlays/local", f))
		b, errB := os.ReadFile(filepath.Join(repoRoot, "gitops/overlays/cloud", f))
		if errA != nil || errB != nil || string(a) != string(b) {
			t.Errorf("%s differs between the local and cloud overlays (%v, %v)", f, errA, errB)
		}
	}
	// Every stack with a local overlay has a cloud one.
	entries, err := os.ReadDir(filepath.Join(repoRoot, "gitops/overlays/local"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(repoRoot, "gitops/overlays/cloud", e.Name())); err != nil {
			t.Errorf("the cloud overlay lacks %s", e.Name())
		}
	}
	if !slices.Contains(overlayModes, "cloud") {
		t.Error("cloud is not an overlay mode")
	}
}

// In cloud mode every long-running container of the platform charts asks
// for memory, so Karpenter sizes nodes for it: without requests it packed
// Argo CD and the core charts onto a t4g.micro (session 4). Prometheus and
// Alertmanager pods come from the operator's CRs, checked through their
// spec.resources. Jobs (hooks, one-off migrations) are left out.
func TestRenderCloudMemoryRequests(t *testing.T) {
	checked := 0
	for _, o := range renderMode(t, "cloud") {
		var containers []any
		switch o.Obj["kind"] {
		case "Deployment", "StatefulSet", "DaemonSet":
			containers, _ = field(o.Obj, "spec", "template", "spec", "containers").([]any)
		case "Prometheus", "Alertmanager":
			containers = []any{map[string]any{"name": "main", "resources": field(o.Obj, "spec", "resources")}}
		default:
			continue
		}
		for _, c := range containers {
			m := c.(map[string]any)
			checked++
			if field(m, "resources", "requests", "memory") == nil {
				t.Errorf("%s (%s) container %v has no memory request", kindName(o.Obj), o.App, m["name"])
			}
		}
	}
	if checked < 20 {
		t.Errorf("checked %d containers, want the platform charts' (20+)", checked)
	}
}
