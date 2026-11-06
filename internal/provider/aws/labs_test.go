package aws

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLabsEnv(t *testing.T) {
	exp := time.Date(2026, 11, 6, 14, 0, 0, 0, time.UTC)
	env, err := LabsEnv("123456789012", map[string]string{
		"AWS_REGION": Region, "AWS_ACCESS_KEY_ID": "AKIA", "AWS_SECRET_ACCESS_KEY": "s", "AWS_SESSION_TOKEN": "tok",
	}, LabTags("someone", exp))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"LAB_REGISTRY_PUSH=123456789012.dkr.ecr.ap-south-1.amazonaws.com/lab",
		"LAB_REGISTRY_PULL=123456789012.dkr.ecr.ap-south-1.amazonaws.com/lab",
		"LAB_PLATFORM=linux/arm64",
		`LAB_AWS_TAGS=[{"Key":"component","Value":"labs"},{"Key":"expires-at","Value":"2026-11-06T14:00:00Z"},{"Key":"lab","Value":"true"},{"Key":"owner","Value":"someone"}]`,
		"AWS_ACCESS_KEY_ID=AKIA", "AWS_REGION=ap-south-1", "AWS_SECRET_ACCESS_KEY=s", "AWS_SESSION_TOKEN=tok",
	}
	if !slices.Equal(env, want) {
		t.Errorf("env:\n%s\nwant:\n%s", strings.Join(env, "\n"), strings.Join(want, "\n"))
	}
	for _, e := range env {
		if strings.HasPrefix(e, "AWS_ENDPOINT_URL") {
			t.Errorf("cloud labs must reach real AWS, got %s", e)
		}
	}
}

func TestProviderLabsEnv(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		expiry  string // "" writes no file
		wantErr string
	}{
		{"no cloud lab", "", "no cloud lab is running"},
		{"expired", "2026-11-06T11:00:00Z", "expired"},
		{"running", "2026-11-06T14:00:00Z", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if tt.expiry != "" {
				if err := os.WriteFile(filepath.Join(home, "expires-at"), []byte(tt.expiry+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			p := &Provider{opts: Options{Home: home, Owner: "someone"}, account: "123456789012",
				env: map[string]string{"AWS_REGION": Region}, now: func() time.Time { return now }}
			env, err := p.LabsEnv()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(env, "AWS_REGION=ap-south-1") || !strings.Contains(strings.Join(env, " "), `"2026-11-06T14:00:00Z"`) {
				t.Errorf("env = %v", env)
			}
		})
	}
}
