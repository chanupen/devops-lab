package aws

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

var testRole = Role{
	ARN:       "arn:aws:iam::111122223333:role/lab-admin",
	MFASerial: "arn:aws:iam::111122223333:mfa/me",
	Duration:  4 * time.Hour,
}

func fixedRole(Role) func(context.Context, string) (Role, error) {
	return func(context.Context, string) (Role, error) { return testRole, nil }
}

// writeCLICache writes a session the way the aws CLI does.
func writeCLICache(t *testing.T, dir string, expires string) {
	t.Helper()
	body := `{"Credentials": {"AccessKeyId": "ASIA1", "SecretAccessKey": "secret", "SessionToken": "token", "Expiration": "` +
		expires + `"}, "AssumedRoleUser": {"Arn": "arn:aws:sts::111122223333:assumed-role/lab-admin/botocore-session-1"}}`
	if err := os.WriteFile(filepath.Join(dir, CacheKey(testRole)+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The file names must match what botocore computes, or lab and the aws CLI
// would each ask for their own MFA code. Expected values come from
// sha1(json.dumps(args, sort_keys=True)) in Python.
func TestCacheKeyMatchesTheAWSCLI(t *testing.T) {
	tests := []struct {
		role Role
		want string
	}{
		{testRole, "40f9ec459e9e5c8f997ba075692bd19e3ac98f32"},
		{Role{ARN: "arn:aws:iam::111122223333:role/lab-admin"}, "13c7111c6d10e497713b42a4f595981802c44319"},
		{Role{ARN: "arn:aws:iam::111122223333:role/x", ExternalID: "e1", MFASerial: "s"}, "2b73c03597eb9ca22d5615cfbb12768850300484"},
	}
	for _, tt := range tests {
		if got := CacheKey(tt.role); got != tt.want {
			t.Errorf("CacheKey(%+v) = %s, want %s", tt.role, got, tt.want)
		}
	}
}

func TestSessionCache(t *testing.T) {
	tests := []struct {
		name    string
		expires string
		corrupt bool
		valid   bool
	}{
		{"fresh session is used", "2026-11-01T15:00:00+00:00", false, true},
		{"the Z form is read too", "2026-11-01T15:00:00Z", false, true},
		{"one expiring within five minutes is not", "2026-11-01T12:04:00+00:00", false, false},
		{"an expired one is not", "2026-11-01T11:59:00+00:00", false, false},
		{"a corrupt file is not", "2026-11-01T15:00:00+00:00", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			s := Session{Profile: "lab", CacheDir: dir, Now: func() time.Time { return t0 }, Role: fixedRole(testRole)}
			writeCLICache(t, dir, tt.expires)
			if tt.corrupt {
				_ = os.WriteFile(filepath.Join(dir, CacheKey(testRole)+".json"), []byte("{"), 0o600)
			}
			_, ok := s.readCache(testRole)
			if ok != tt.valid {
				t.Errorf("readCache ok = %v, want %v", ok, tt.valid)
			}
		})
	}
}

func TestSessionConfigUsesTheCLICache(t *testing.T) {
	dir := t.TempDir()
	writeCLICache(t, dir, time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05-07:00"))
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "no-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "no-credentials"))
	prompted := false
	s := Session{Profile: "lab", CacheDir: dir, Role: fixedRole(testRole),
		Prompt: func(string) (string, error) { prompted = true; return "", nil }}
	cfg, err := s.Config(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.Env(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if prompted || env["AWS_ACCESS_KEY_ID"] != "ASIA1" || env["AWS_SESSION_TOKEN"] != "token" || env["AWS_REGION"] != Region {
		t.Errorf("prompted %v, env %v", prompted, env)
	}
	if _, ok := s.Expires(context.Background()); !ok {
		t.Error("Expires found no session")
	}
	if err := s.Forget(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, CacheKey(testRole)+".json")); !os.IsNotExist(err) {
		t.Error("Forget left the cache file")
	}
}

// A session lab writes must read back, private, in the CLI's layout.
func TestWriteCacheRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	s := Session{Profile: "lab", CacheDir: dir, Now: func() time.Time { return t0 }}
	s.writeCache(testRole, awssdk.Credentials{AccessKeyID: "ASIA1", SecretAccessKey: "secret", SessionToken: "token",
		CanExpire: true, Expires: t0.Add(4 * time.Hour)})
	path := filepath.Join(dir, CacheKey(testRole)+".json")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("cache mode %v, want 0600", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"Expiration":"2026-11-01T16:00:00+00:00"`) {
		t.Errorf("cache file = %s", b)
	}
	c, ok := s.readCache(testRole)
	if !ok || c.AccessKeyID != "ASIA1" || c.SessionToken != "token" {
		t.Errorf("read back %+v, ok %v", c, ok)
	}
}

func TestSyncModule(t *testing.T) {
	mod, work := t.TempDir(), t.TempDir()
	for _, f := range []string{"main.tf", "state.tf", "README.md", "policy.json"} {
		if err := os.WriteFile(filepath.Join(mod, f), []byte("# "+f), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"old.tf", "backend.tf", "terraform.tfstate"} {
		if err := os.WriteFile(filepath.Join(work, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b := &Baseline{ModuleDir: mod, WorkDir: work}
	if err := b.syncModule(); err != nil {
		t.Fatal(err)
	}
	got, _ := filepath.Glob(filepath.Join(work, "*"))
	for i := range got {
		got[i] = filepath.Base(got[i])
	}
	// Module .tf and .json files copied, stale .tf removed, generated
	// backend and the local state kept, other files not copied.
	if want := []string{"backend.tf", "main.tf", "policy.json", "state.tf", "terraform.tfstate"}; !slices.Equal(got, want) {
		t.Errorf("work dir = %v, want %v", got, want)
	}

	if err := os.WriteFile(filepath.Join(mod, "backend.tf"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.syncModule(); err == nil {
		t.Error("a backend.tf in the module must be refused: lab generates it")
	}
	if err := (&Baseline{ModuleDir: t.TempDir(), WorkDir: work}).syncModule(); err == nil {
		t.Error("an empty module must be refused")
	}
}

// tofu gets the session's credentials and nothing else AWS- or TF-related
// from the caller's environment, so a stray AWS_PROFILE cannot redirect it.
func TestBaselineEnv(t *testing.T) {
	t.Setenv("AWS_PROFILE", "someone-else")
	t.Setenv("TF_LOG", "trace")
	t.Setenv("LAB_KEEP", "1")
	b := &Baseline{Env: map[string]string{"AWS_ACCESS_KEY_ID": "AKIA1", "AWS_REGION": Region}}
	env := b.env()
	if _, ok := env["AWS_PROFILE"]; ok {
		t.Error("AWS_PROFILE reached tofu")
	}
	if _, ok := env["TF_LOG"]; ok {
		t.Error("TF_LOG reached tofu")
	}
	if env["LAB_KEEP"] != "1" || env["AWS_ACCESS_KEY_ID"] != "AKIA1" || env["PATH"] == "" {
		t.Errorf("env = %v", env)
	}
}

func TestStateBucket(t *testing.T) {
	if got := StateBucket("111122223333"); got != "lab-tofu-state-111122223333" {
		t.Errorf("StateBucket = %q", got)
	}
	// The module computes the same name; keep them in step.
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "infra", "terraform", "aws", "baseline", "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"lab-tofu-state-${local.account}"`) {
		t.Error("main.tf does not name the state bucket lab-tofu-state-<account>")
	}
}
