package aws

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBrokenProfileChain reproduces the 2026-11-05 failure: [profile
// devops] gained source_profile = devops, so the SDK rejects the lab
// profile before lab can look for a cached session. The error must say
// which file, what to check and what to run.
func TestBrokenProfileChain(t *testing.T) {
	const lab = `[profile lab]
role_arn = arn:aws:iam::111111111111:role/lab-admin
source_profile = devops
mfa_serial = arn:aws:iam::111111111111:mfa/test
`
	tests := []struct {
		name    string
		devops  string
		wantErr bool
	}{
		{"source profile names itself", "[profile devops]\nregion = ap-south-1\nsource_profile = devops\n", true},
		{"source profile with keys only", "[profile devops]\nregion = ap-south-1\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg, creds := filepath.Join(dir, "config"), filepath.Join(dir, "credentials")
			if err := os.WriteFile(cfg, []byte(tt.devops+"\n"+lab), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(creds, []byte("[devops]\naws_access_key_id = AKIAFAKE\naws_secret_access_key = fake\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Session{Profile: "lab", configFiles: []string{cfg}, credentialsFiles: []string{creds}}.role(context.Background())
			var pe *ProfileError
			if got := errors.As(err, &pe); got != tt.wantErr {
				t.Fatalf("ProfileError = %v, want %v (err %v)", got, tt.wantErr, err)
			}
			if !tt.wantErr {
				return
			}
			for _, want := range []string{"profile lab does not resolve in ~/.aws/config", "source_profile requires role_arn"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
			for _, want := range []string{"aws configure list --profile lab", "lab never edits", "lab cloud login"} {
				if !strings.Contains(pe.Fix(), want) {
					t.Errorf("fix %q lacks %q", pe.Fix(), want)
				}
			}
		})
	}
}
