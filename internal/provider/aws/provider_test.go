package aws

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"
)

func TestExpiresAt(t *testing.T) {
	now := t0.Add(30 * time.Second)
	tests := []struct {
		name    string
		file    string // "" for no file
		create  bool
		want    time.Time
		written bool
	}{
		{"first apply starts the TTL, to the minute", "", true, t0.Add(4 * time.Hour), true},
		{"a later apply keeps the expiry", "2026-11-01T14:00:00Z", true, t0.Add(2 * time.Hour), false},
		{"an apply after a lapse starts a new TTL", "2026-11-01T11:00:00Z", true, t0.Add(4 * time.Hour), true},
		{"a destroy keeps a lapsed expiry", "2026-11-01T11:00:00Z", false, t0.Add(-time.Hour), false},
		{"a destroy or plan with no record writes nothing", "", false, t0.Add(4 * time.Hour), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if tt.file != "" {
				if err := os.WriteFile(ExpiryFile(home), []byte(tt.file+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			p := &Provider{opts: Options{Home: home, TTL: 4 * time.Hour}, now: func() time.Time { return now }}
			got, err := p.expiresAt(tt.create)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("expiresAt = %s, want %s", got, tt.want)
			}
			onDisk, err := ReadExpiry(ExpiryFile(home))
			switch {
			case tt.written && (err != nil || !onDisk.Equal(tt.want)):
				t.Errorf("file = %s, %v; want %s", onDisk, err, tt.want)
			case !tt.written && tt.file == "" && err == nil:
				t.Error("an expiry file was written")
			}
		})
	}
}

func TestNextExpiry(t *testing.T) {
	if got := NextExpiry(t0.Add(time.Hour), t0, time.Hour); !got.Equal(t0.Add(2 * time.Hour)) {
		t.Errorf("from a future expiry: %s", got)
	}
	if got := NextExpiry(t0.Add(-3*time.Hour), t0.Add(20*time.Second), time.Hour); !got.Equal(t0.Add(time.Hour)) {
		t.Errorf("from a lapsed expiry: %s", got)
	}
}

func TestClearExpiry(t *testing.T) {
	home := t.TempDir()
	if err := WriteExpiry(ExpiryFile(home), t0); err != nil {
		t.Fatal(err)
	}
	if err := ClearExpiry(home); err != nil {
		t.Fatal(err)
	}
	if err := ClearExpiry(home); err != nil {
		t.Errorf("clearing twice: %v", err)
	}
}

var info = ClusterInfo{Name: "lab", Endpoint: "https://ABC.gr7.ap-south-1.eks.amazonaws.com", CA: "Y2E=", VPCID: "vpc-1",
	KarpenterQueue: "lab-karpenter", KarpenterNodeRole: "lab-karpenter-node"}

func field(t *testing.T, u *unstructured.Unstructured, path ...string) any {
	t.Helper()
	v, ok, err := unstructured.NestedFieldNoCopy(u.Object, path...)
	if !ok || err != nil {
		t.Fatalf("%s has no %s", u.GetKind(), strings.Join(path, "."))
	}
	return v
}

func TestSpotNodePool(t *testing.T) {
	np := SpotNodePool()
	if got := field(t, np, "spec", "limits", "cpu"); got != "8" {
		t.Errorf("cpu limit = %v", got)
	}
	if got := field(t, np, "spec", "disruption", "consolidationPolicy"); got != "WhenEmptyOrUnderutilized" {
		t.Errorf("consolidation = %v", got)
	}
	reqs := field(t, np, "spec", "template", "spec", "requirements").([]any)
	want := map[string]string{"kubernetes.io/arch": "arm64", "karpenter.sh/capacity-type": "spot"}
	for _, r := range reqs {
		m := r.(map[string]any)
		if v, ok := want[m["key"].(string)]; ok {
			if !slices.Equal(m["values"].([]any), []any{v}) || m["operator"] != "In" {
				t.Errorf("requirement %v", m)
			}
			delete(want, m["key"].(string))
		}
	}
	if len(want) > 0 {
		t.Errorf("missing requirements %v", want)
	}
	// No micro or small nodes: with prefix delegation they take 32 pods on
	// 1-2 GiB.
	memFloor := false
	for _, r := range reqs {
		m := r.(map[string]any)
		if m["key"] == "karpenter.k8s.aws/instance-memory" && m["operator"] == "Gt" && slices.Equal(m["values"].([]any), []any{"4000"}) {
			memFloor = true
		}
	}
	if !memFloor {
		t.Error("no 4 GiB memory floor")
	}
	// lab extend changes the node class tags; that must not roll nodes.
	budgets := field(t, np, "spec", "disruption", "budgets").([]any)
	if b := budgets[0].(map[string]any); b["nodes"] != "0" || !slices.Equal(b["reasons"].([]any), []any{"Drifted"}) {
		t.Errorf("first budget = %v, want no drift replacements", b)
	}
}

func TestEC2NodeClassCarriesLabTags(t *testing.T) {
	exp := t0.Add(4 * time.Hour)
	nc := EC2NodeClass(info, LabTags("me", exp))
	tags := field(t, nc, "spec", "tags").(map[string]any)
	for k, v := range map[string]string{"lab": "true", "owner": "me", "expires-at": "2026-11-01T16:00:00Z", "component": "karpenter"} {
		if tags[k] != v {
			t.Errorf("tag %s = %v, want %s", k, tags[k], v)
		}
	}
	if field(t, nc, "spec", "role") != "lab-karpenter-node" {
		t.Error("node class does not use the karpenter node role")
	}
	if field(t, nc, "spec", "metadataOptions", "httpTokens") != "required" {
		t.Error("IMDSv2 not required")
	}
	// Prefix delegation: max pods must not follow the ENI limit.
	if field(t, nc, "spec", "kubelet", "podsPerCore") != int64(16) || field(t, nc, "spec", "kubelet", "maxPods") != int64(110) {
		t.Errorf("kubelet = %v", field(t, nc, "spec", "kubelet"))
	}
}

func TestLBCValuesDefaultTags(t *testing.T) {
	v := LBCValues(info, LabTags("me", t0))
	dt := v["defaultTags"].(map[string]any)
	if dt["expires-at"] != "2026-11-01T12:00:00Z" || dt["lab"] != "true" || dt["owner"] != "me" {
		t.Errorf("defaultTags = %v", dt)
	}
	if v["vpcId"] != "vpc-1" || v["region"] != Region || v["clusterName"] != "lab" {
		t.Errorf("values = %v", v)
	}
	k := KarpenterValues(info)["settings"].(map[string]any)
	if k["interruptionQueue"] != "lab-karpenter" || k["clusterName"] != "lab" {
		t.Errorf("karpenter settings = %v", k)
	}
}

func TestWriteKubeconfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := WriteKubeconfig(path, info, "lab"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("kubeconfig %v, %v", fi, err)
	}
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := cfg.Contexts[cfg.CurrentContext]
	exec := cfg.AuthInfos[ctx.AuthInfo].Exec
	if cfg.Clusters[ctx.Cluster].Server != info.Endpoint || string(cfg.Clusters[ctx.Cluster].CertificateAuthorityData) != "ca" {
		t.Errorf("cluster = %+v", cfg.Clusters[ctx.Cluster])
	}
	args := strings.Join(exec.Args, " ")
	if exec.Command != "aws" || !strings.Contains(args, "eks get-token --cluster-name lab --region ap-south-1 --profile lab") {
		t.Errorf("exec = %s %s", exec.Command, args)
	}
	if err := WriteKubeconfig(path, ClusterInfo{CA: "not base64!"}, "lab"); err == nil {
		t.Error("a bad CA must fail")
	}
}

func TestParseIPv4(t *testing.T) {
	for in, want := range map[string]string{"203.0.113.7\n": "203.0.113.7", "198.51.100.1\r\n": "198.51.100.1"} {
		if got, err := parseIPv4(in); err != nil || got != want {
			t.Errorf("parseIPv4(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "2001:db8::1", "<html>"} {
		if _, err := parseIPv4(in); err == nil {
			t.Errorf("parseIPv4(%q) accepted", in)
		}
	}
}

func TestBatches(t *testing.T) {
	s := make([]int, 45)
	var sizes []int
	for _, b := range batches(s, 20) {
		sizes = append(sizes, len(b))
	}
	if !slices.Equal(sizes, []int{20, 20, 5}) {
		t.Errorf("batch sizes = %v", sizes)
	}
	if len(batches([]int{}, 20)) != 0 {
		t.Error("empty input made a batch")
	}
}

func TestWithoutBaseline(t *testing.T) {
	ts := []Tagged{
		{ARN: "a", Tags: map[string]string{"component": "baseline"}},
		{ARN: "b", Tags: map[string]string{"component": "cluster"}},
		{ARN: "c", Tags: map[string]string{}}, // e.g. a controller-made load balancer
	}
	var got []string
	for _, t := range withoutBaseline(ts) {
		got = append(got, t.ARN)
	}
	if !slices.Equal(got, []string{"b", "c"}) {
		t.Errorf("leftovers = %v", got)
	}
}

func TestShortARN(t *testing.T) {
	if got := ShortARN("arn:aws:eks:ap-south-1:111122223333:cluster/lab"); got != "eks:cluster/lab" {
		t.Errorf("ShortARN = %s", got)
	}
}

func TestSupports(t *testing.T) {
	p := &Provider{}
	if !p.Supports("registry") || !p.Supports("cluster") || p.Supports("gitops") {
		t.Error("the cloud provider runs registry and cluster only")
	}
}

// The Go side passes variables and reads outputs by name; keep them in step
// with the modules.
func TestModulesMatchTheProvider(t *testing.T) {
	repo := filepath.Join("..", "..", "..")
	read := func(dir string) string {
		files, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
		var b strings.Builder
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(data)
		}
		return b.String()
	}
	cluster, registry := read(ClusterModuleDir(repo)), read(RegistryModuleDir(repo))
	p := &Provider{opts: Options{Owner: "me"}, version: "1.36"}
	check := func(name, src string, vars map[string]any, outputs ...string) {
		for v := range vars {
			if !regexp.MustCompile(`variable "` + v + `"`).MatchString(src) {
				t.Errorf("%s module has no variable %s", name, v)
			}
		}
		for _, o := range outputs {
			if !strings.Contains(src, `output "`+o+`"`) {
				t.Errorf("%s module has no output %s", name, o)
			}
		}
	}
	check("cluster", cluster, p.clusterModule(t0, []string{"203.0.113.7/32"}, io.Discard).Vars,
		"cluster_name", "cluster_endpoint", "cluster_ca", "vpc_id", "karpenter_queue", "karpenter_node_role")
	check("registry", registry, p.registryModule(t0, io.Discard).Vars, "lab_registry")
	// ECR makes no repository on push: every image the lab pushes has one.
	for _, repo := range []string{SampleDBRepo, "lab02-hello"} {
		if !regexp.MustCompile(`default *= \[[^\]]*"` + repo + `"`).MatchString(registry) {
			t.Errorf("registry module's repositories do not include %s", repo)
		}
	}
	if !strings.Contains(registry, `default     = "lab"`) || LabRegistryFor("123456789012") != "123456789012.dkr.ecr.ap-south-1.amazonaws.com/lab" {
		t.Error("the lab registry is not <registry>/lab")
	}
	if !strings.Contains(cluster, `default     = "`+ClusterName+`"`) {
		t.Errorf("cluster module's default cluster_name is not %q", ClusterName)
	}
	for _, src := range []string{cluster, registry} {
		if !strings.Contains(src, `"expires-at" = var.expires_at`) {
			t.Error("a module does not tag expires-at")
		}
	}
}
