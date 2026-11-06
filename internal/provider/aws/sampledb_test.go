package aws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type fakeSecrets struct {
	values  map[string]string
	tags    map[string]map[string]string
	deleted []string
	failDel error
}

func (f *fakeSecrets) Get(_ context.Context, name string) (string, bool, error) {
	v, ok := f.values[name]
	return v, ok, nil
}

func (f *fakeSecrets) Create(_ context.Context, name, value string, tags map[string]string) error {
	f.values[name], f.tags[name] = value, tags
	return nil
}

func (f *fakeSecrets) Delete(_ context.Context, name string) error {
	if f.failDel != nil {
		return f.failDel
	}
	delete(f.values, name)
	f.deleted = append(f.deleted, name)
	return nil
}

func TestSeedAndRemoveSampleDB(t *testing.T) {
	exp := time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)
	f := &fakeSecrets{values: map[string]string{SampleDBAdminSecret: `{"username":"postgres","password":"kept"}`},
		tags: map[string]map[string]string{}}
	tags := SampleDBTags("someone", exp)
	if err := SeedSampleDB(context.Background(), f, tags, io.Discard); err != nil {
		t.Fatal(err)
	}
	if f.values[SampleDBAdminSecret] != `{"username":"postgres","password":"kept"}` {
		t.Error("an existing secret was overwritten")
	}
	var app map[string]string
	if err := json.Unmarshal([]byte(f.values[SampleDBSecret]), &app); err != nil || app["username"] != SampleDBUser || len(app["password"]) != 36 {
		t.Errorf("app secret = %q (%v)", f.values[SampleDBSecret], err)
	}
	want := map[string]string{"lab": "true", "owner": "someone", "expires-at": "2026-11-05T14:00:00Z", "component": "sample-db"}
	for k, v := range want {
		if f.tags[SampleDBSecret][k] != v {
			t.Errorf("tag %s = %q, want %q", k, f.tags[SampleDBSecret][k], v)
		}
	}
	if err := RemoveSampleDB(context.Background(), f, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(f.values) != 0 || len(f.deleted) != 2 {
		t.Errorf("after remove: %v left, deleted %v", f.values, f.deleted)
	}
	f.failDel = errors.New("denied")
	if err := RemoveSampleDB(context.Background(), f, io.Discard); err == nil || !strings.Contains(err.Error(), SampleDBSecret) {
		t.Errorf("a failed delete: %v", err)
	}
}

func TestEBSCSIValues(t *testing.T) {
	tags := LabTags("someone", time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC))
	v := EBSCSIValues(tags)
	ctrl := v["controller"].(map[string]any)
	vt := ctrl["extraVolumeTags"].(map[string]any)
	for k, want := range tags {
		if vt[k] != want {
			t.Errorf("extraVolumeTags[%s] = %v, want %s", k, vt[k], want)
		}
	}
	if ctrl["serviceAccount"].(map[string]any)["name"] != "ebs-csi-controller-sa" {
		t.Error("the controller's service account is not the one Pod Identity binds")
	}
	sc := v["storageClasses"].([]any)[0].(map[string]any)
	if sc["name"] != GP3StorageClass || sc["reclaimPolicy"] != "Delete" ||
		sc["annotations"].(map[string]any)["storageclass.kubernetes.io/is-default-class"] != "true" ||
		sc["parameters"].(map[string]any)["type"] != "gp3" {
		t.Errorf("storage class = %v", sc)
	}
}

func TestCheckStacksOn(t *testing.T) {
	tests := []struct {
		v      string
		stacks []string
		want   string // "" = allowed
	}{
		{"1.36", []string{"policy", "secrets"}, ""},
		{"1.37", []string{"sample-app"}, ""},
		{"1.37", []string{"policy"}, "Kyverno (stack policy)"},
		{"1.35", []string{"secrets", "obs"}, "External Secrets Operator (stack secrets)"},
		{"1.37", []string{"gitops", "policy"}, "Argo CD (stack gitops)"},
	}
	for _, tt := range tests {
		err := CheckStacksOn(tt.v, tt.stacks)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s %v: %v", tt.v, tt.stacks, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "lab down --mode cloud")):
			t.Errorf("%s %v: %v, want %q", tt.v, tt.stacks, err, tt.want)
		}
	}
}
