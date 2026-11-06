package local

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/chanupen/devops-lab/internal/kube"
)

// fakeBuckets is S3 in memory.
type fakeBuckets struct{ made []string }

func (f *fakeBuckets) Ensure(_ context.Context, name string) (bool, error) {
	if slices.Contains(f.made, name) {
		return false, nil
	}
	f.made = append(f.made, name)
	return true, nil
}

func TestOllamaAddress(t *testing.T) {
	onKind := map[string]string{KindNetwork: "10.89.1.250"}
	loopback := map[string][]string{"11434/tcp": {"127.0.0.1:11434"}}
	tests := []struct {
		name    string
		ollama  *ContainerInfo
		want    string
		wantErr []string
	}{
		{"missing", nil, "", []string{"no ollama container", OllamaQuadlet}},
		{"stopped", &ContainerInfo{Exists: true}, "", []string{"systemctl --user start ollama"}},
		{"on the LAN", &ContainerInfo{Exists: true, Running: true, Networks: onKind, HostPorts: map[string][]string{"11434/tcp": {"0.0.0.0:11434"}}},
			"", []string{"0.0.0.0:11434", "PublishPort=127.0.0.1:11434:11434"}},
		{"empty host IP is every address", &ContainerInfo{Exists: true, Running: true, Networks: onKind, HostPorts: map[string][]string{"11434/tcp": {":11434"}}},
			"", []string{"which the LAN can reach"}},
		{"not on the kind network", &ContainerInfo{Exists: true, Running: true, HostPorts: loopback, Networks: map[string]string{"pasta": ""}},
			"", []string{"not on the kind network", "Network=kind:ip=10.89.1.250", "NetworkAlias=lab-ollama"}},
		{"ready", &ContainerInfo{Exists: true, Running: true, HostPorts: loopback, Networks: onKind}, "10.89.1.250", nil},
		{"not published at all", &ContainerInfo{Exists: true, Running: true, Networks: onKind}, "10.89.1.250", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newFakeRuntime()
			rt.containers[ZotContainer] = ContainerInfo{Exists: true, Running: true, Networks: map[string]string{KindNetwork: "10.89.1.5"}}
			if tt.ollama != nil {
				rt.containers[OllamaContainer] = *tt.ollama
			}
			p := testProvider(t, rt, &fakeClusters{}, false)
			got, err := p.OllamaAddress(context.Background())
			if got != tt.want {
				t.Errorf("address = %q, want %q", got, tt.want)
			}
			for _, w := range tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to contain %q", err, w)
				}
			}
			if tt.wantErr == nil && err != nil {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestWriteOllamaEndpoints(t *testing.T) {
	ctx := context.Background()
	core := fake.NewClientset()
	for _, addr := range []string{"10.89.1.250", "10.89.1.250", "10.89.1.251"} {
		if err := writeOllamaEndpoints(ctx, core, addr, io.Discard); err != nil {
			t.Fatal(err)
		}
		s, err := core.DiscoveryV1().EndpointSlices(AINamespace).Get(ctx, OllamaSlice, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if SliceAddress(s) != addr || s.Labels["kubernetes.io/service-name"] != OllamaService ||
			s.Labels["endpointslice.kubernetes.io/managed-by"] == "endpointslice-controller.k8s.io" || *s.Ports[0].Port != OllamaPort {
			t.Errorf("EndpointSlice = %+v, want %s", s, addr)
		}
	}
	if _, err := core.CoreV1().Namespaces().Get(ctx, AINamespace, metav1.GetOptions{}); err != nil {
		t.Errorf("namespace ai: %v", err)
	}
}

// SeedAI creates Langfuse's four secrets once, with the fields the chart
// reads, and the S3 bucket; a second run keeps them.
func TestSeedAI(t *testing.T) {
	rt := newFakeRuntime()
	p := testProvider(t, rt, &fakeClusters{}, false)
	s := &fakeSecrets{m: map[string]string{}}
	b := &fakeBuckets{}
	p.secrets, p.buckets = s, b
	if err := p.SeedAI(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	first := map[string]string{}
	for k, v := range s.m {
		first[k] = v
	}
	want := map[string][]string{
		LangfuseAppSecret:        {"salt", "encryption-key", "nextauth-secret", "public-key", "secret-key", "admin-password"},
		LangfusePostgresSecret:   {"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB", "USERDB_USER", "USERDB_PASSWORD"},
		LangfuseRedisSecret:      {"default"},
		LangfuseClickHouseSecret: {"password"},
	}
	for name, keys := range want {
		var m map[string]string
		if err := json.Unmarshal([]byte(s.m[name]), &m); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, k := range keys {
			if m[k] == "" {
				t.Errorf("%s lacks %s", name, k)
			}
		}
		if len(m) != len(keys) {
			t.Errorf("%s has %d fields, want %d", name, len(m), len(keys))
		}
		if name == LangfuseAppSecret {
			if len(m["encryption-key"]) != 64 || !strings.HasPrefix(m["public-key"], "pk-lf-") || !strings.HasPrefix(m["secret-key"], "sk-lf-") {
				t.Errorf("%s = %v", name, m)
			}
		}
		if name == LangfusePostgresSecret && m["USERDB_PASSWORD"] != m["POSTGRES_PASSWORD"] {
			t.Errorf("%s: the two logins differ", name)
		}
	}
	if !slices.Equal(b.made, []string{LangfuseBucket}) {
		t.Errorf("buckets = %v", b.made)
	}
	if err := p.SeedAI(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	for k, v := range first {
		if s.m[k] != v {
			t.Errorf("%s changed on the second run", k)
		}
	}
}

// After Moto restarts, lab up makes the Langfuse bucket again while the ai
// stack is there, and not otherwise.
func TestReseedMakesLangfuseBucket(t *testing.T) {
	for _, withAI := range []bool{true, false} {
		rt := newFakeRuntime()
		rt.containers[MotoContainer] = ContainerInfo{Exists: true, Running: true}
		p := testProvider(t, rt, &fakeClusters{nodes: map[string]int{}}, false)
		b := &fakeBuckets{}
		p.secrets, p.buckets = &fakeSecrets{m: map[string]string{}}, b
		var objs []runtime.Object
		if withAI {
			objs = append(objs, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: AINamespace}})
		}
		core := fake.NewClientset(objs...)
		dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
			map[schema.GroupVersionResource]string{ExternalSecretGVR: "ExternalSecretList"})
		p.kube = func() (*kube.Clients, error) { return &kube.Clients{Core: core, Dynamic: dyn}, nil }
		if _, err := p.StartStopped(context.Background(), io.Discard); err != nil {
			t.Fatal(err)
		}
		if got := len(b.made) == 1; got != withAI {
			t.Errorf("ai stack %v: bucket made = %v", withAI, b.made)
		}
	}
}

func TestNonLoopback(t *testing.T) {
	got := NonLoopback(map[string][]string{"a": {"127.0.0.1:1", "[::1]:2", "0.0.0.0:3", "192.168.50.50:4", "[::]:5", ":6"}})
	slices.Sort(got)
	want := []string{"0.0.0.0:3", "192.168.50.50:4", ":6", "[::]:5"}
	if !slices.Equal(got, want) {
		t.Errorf("NonLoopback = %v, want %v", got, want)
	}
}
