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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/chanupen/devops-lab/internal/kube"
)

// fakeSecrets is Secrets Manager in memory.
type fakeSecrets struct{ m map[string]string }

func (f *fakeSecrets) Get(_ context.Context, name string) (string, bool, error) {
	v, ok := f.m[name]
	return v, ok, nil
}
func (f *fakeSecrets) Put(_ context.Context, name, value string) error { f.m[name] = value; return nil }
func (f *fakeSecrets) Count(context.Context) (int, error)              { return len(f.m), nil }

func externalSecret(ns, name, store, target string, data []any, dataFrom []any) *unstructured.Unstructured {
	spec := map[string]any{"secretStoreRef": map[string]any{"kind": "ClusterSecretStore", "name": store}}
	if target != "" {
		spec["target"] = map[string]any{"name": target}
	}
	if data != nil {
		spec["data"] = data
	}
	if dataFrom != nil {
		spec["dataFrom"] = dataFrom
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "external-secrets.io/v1", "kind": "ExternalSecret",
		"metadata": map[string]any{"name": name, "namespace": ns}, "spec": spec,
	}}
}

// templated is an ExternalSecret whose target.template builds the Secret.
func templated(ns, name, key string) *unstructured.Unstructured {
	es := externalSecret(ns, name, SecretStore, "", []any{ref("password", key, "password")}, nil)
	es.Object["spec"].(map[string]any)["target"] = map[string]any{"template": map[string]any{
		"data": map[string]any{"dsn": "postgres://app:{{ .password }}@postgres/app"},
	}}
	return es
}

func ref(secretKey, key, prop string) map[string]any {
	return map[string]any{"secretKey": secretKey, "remoteRef": map[string]any{"key": key, "property": prop}}
}

func secret(ns, name string, data map[string]string) *corev1.Secret {
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Data: map[string][]byte{}}
	for k, v := range data {
		s.Data[k] = []byte(v)
	}
	return s
}

// After a restart Moto is empty while ESO's Secrets still hold the values
// the databases use: Reseed puts them back, and leaves current ones alone.
func TestReseed(t *testing.T) {
	ctx := context.Background()
	objs := []runtime.Object{
		// Two properties of one key, by data[].
		externalSecret("lab04-app", "db-credentials", SecretStore, "",
			[]any{ref("username", "lab04/app", "username"), ref("password", "lab04/app", "password")}, nil),
		// A whole key, by dataFrom.extract, into a differently named Secret.
		externalSecret("sample-db", "db-app", SecretStore, "app-db",
			nil, []any{map[string]any{"extract": map[string]any{"key": "lab/sample-db/app"}}}),
		// Another store: not Moto's to fill.
		externalSecret("other", "x", "vault", "", []any{ref("k", "other/x", "k")}, nil),
		// Not synced yet: no Secret, nothing to put back.
		externalSecret("lab04-app", "pending", SecretStore, "", []any{ref("k", "lab04/pending", "k")}, nil),
		// A template builds the Secret from the remote value (here a DSN),
		// so the Secret is not the value: skipped, with a warning.
		templated("lab04-app", "dsn", "lab04/dsn"),
	}
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{ExternalSecretGVR: "ExternalSecretList"}, objs...)
	core := fake.NewClientset(
		secret("lab04-app", "db-credentials", map[string]string{"username": "app_v2", "password": "p2"}),
		secret("sample-db", "app-db", map[string]string{"username": "app", "password": "pa"}),
		secret("other", "x", map[string]string{"k": "v"}),
		secret("lab04-app", "dsn", map[string]string{"dsn": "postgres://app:pw@postgres/app"}),
	)
	tests := []struct {
		name   string
		before map[string]string
		put    []string
		want   map[string]map[string]string
	}{
		{"empty after a restart", map[string]string{}, []string{"lab/sample-db/app", "lab04/app"}, map[string]map[string]string{
			"lab04/app":         {"username": "app_v2", "password": "p2"},
			"lab/sample-db/app": {"username": "app", "password": "pa"},
		}},
		{"a current value is kept", map[string]string{"lab04/app": `{"username":"app_v3","password":"p3"}`}, []string{"lab/sample-db/app"},
			map[string]map[string]string{"lab04/app": {"username": "app_v3", "password": "p3"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeSecrets{m: tt.before}
			put, skipped, err := Reseed(ctx, s, dyn, core)
			if err != nil || !slices.Equal(put, tt.put) {
				t.Fatalf("Reseed put %v, %v; want %v", put, err, tt.put)
			}
			if want := []string{"lab04-app/dsn"}; !slices.Equal(skipped, want) {
				t.Errorf("Reseed skipped %v, want %v", skipped, want)
			}
			if _, ok := s.m["lab04/dsn"]; ok {
				t.Error("put a templated Secret's value back as if it were the remote value")
			}
			for key, want := range tt.want {
				var got map[string]string
				if err := json.Unmarshal([]byte(s.m[key]), &got); err != nil {
					t.Fatalf("%s = %q: %v", key, s.m[key], err)
				}
				for k, v := range want {
					if got[k] != v {
						t.Errorf("%s.%s = %q, want %q", key, k, got[k], v)
					}
				}
			}
			if _, ok := s.m["other/x"]; ok {
				t.Error("filled a secret of another store")
			}
			if _, ok := s.m["lab04/pending"]; ok {
				t.Error("filled a secret ESO has not synced")
			}
		})
	}
}

func TestSeedCredentials(t *testing.T) {
	ctx := context.Background()
	s := &fakeSecrets{m: map[string]string{}}
	created, err := SeedCredentials(ctx, s, "lab/sample-db/app", "app")
	if err != nil || !created {
		t.Fatalf("first seed: %v, %v", created, err)
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(s.m["lab/sample-db/app"]), &v); err != nil || v["username"] != "app" || len(v["password"]) < 24 {
		t.Fatalf("seeded %q (%v)", s.m["lab/sample-db/app"], err)
	}
	first := s.m["lab/sample-db/app"]
	if created, err := SeedCredentials(ctx, s, "lab/sample-db/app", "app"); err != nil || created || s.m["lab/sample-db/app"] != first {
		t.Errorf("second seed changed it: %v, %v", created, err)
	}
}

// The secrets stack starts Moto on the kind network, on 127.0.0.1 only.
func TestEnsureMoto(t *testing.T) {
	tests := []struct {
		name    string
		before  *ContainerInfo
		created bool
		started bool
	}{
		{"not there", nil, true, true},
		{"stopped", &ContainerInfo{Exists: true}, false, true},
		{"running", &ContainerInfo{Exists: true, Running: true}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newFakeRuntime()
			rt.networks[KindNetwork] = true
			if tt.before != nil {
				rt.containers[MotoContainer] = *tt.before
			}
			p := testProvider(t, rt, &fakeClusters{nodes: map[string]int{}}, false)
			p.secrets = &fakeSecrets{m: map[string]string{}}
			if err := p.Apply(context.Background(), "secrets", io.Discard); err != nil {
				t.Fatal(err)
			}
			calls := strings.Join(rt.calls, " ")
			if strings.Contains(calls, "CreateContainer") != tt.created || strings.Contains(calls, "StartContainer") != tt.started {
				t.Errorf("calls = %s", calls)
			}
			if tt.created {
				c := rt.created[0]
				if c.Name != MotoContainer || c.Image != MotoImage || c.Network != KindNetwork || len(c.Ports) != 1 ||
					c.Ports[0].HostIP != "127.0.0.1" || c.Ports[0].HostPort != MotoHostPort {
					t.Errorf("spec = %+v", c)
				}
			}
			if err := p.Destroy(context.Background(), "secrets", io.Discard); err != nil || rt.containers[MotoContainer].Exists {
				t.Errorf("destroy: %v, %+v", err, rt.containers[MotoContainer])
			}
		})
	}
}

// lab up re-seeds Moto whenever it runs: after it started it, and after
// someone restarted it by hand.
func TestStartStoppedReseeds(t *testing.T) {
	tests := []struct {
		name   string
		moto   *ContainerInfo
		reseed bool
	}{
		{"stopped, then started by lab up", &ContainerInfo{Exists: true}, true},
		{"restarted by hand, empty", &ContainerInfo{Exists: true, Running: true}, true},
		{"no Moto (no secrets stack)", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newFakeRuntime()
			if tt.moto != nil {
				rt.containers[MotoContainer] = *tt.moto
			}
			p := testProvider(t, rt, &fakeClusters{nodes: map[string]int{}}, false)
			s := &fakeSecrets{m: map[string]string{}}
			p.secrets = s
			dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
				map[schema.GroupVersionResource]string{ExternalSecretGVR: "ExternalSecretList"},
				externalSecret("lab04-app", "db-credentials", SecretStore, "",
					[]any{ref("username", "lab04/app", "username"), ref("password", "lab04/app", "password")}, nil))
			core := fake.NewClientset(secret("lab04-app", "db-credentials", map[string]string{"username": "app_v1", "password": "p1"}))
			p.kube = func() (*kube.Clients, error) { return &kube.Clients{Core: core, Dynamic: dyn}, nil }
			var log strings.Builder
			if _, err := p.StartStopped(context.Background(), &log); err != nil {
				t.Fatal(err)
			}
			if _, ok := s.m["lab04/app"]; ok != tt.reseed {
				t.Errorf("re-seeded = %v, want %v (log %q)", ok, tt.reseed, log.String())
			}
			if tt.reseed && !strings.Contains(log.String(), "put lab04/app back into lab-moto") {
				t.Errorf("log: %s", log.String())
			}
		})
	}
}
