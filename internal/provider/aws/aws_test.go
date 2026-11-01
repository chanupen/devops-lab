package aws

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func writeCache(t *testing.T, dir string, c cached) {
	t.Helper()
	b, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(dir, "lab.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCache(t *testing.T) {
	tests := []struct {
		name    string
		expires time.Time
		corrupt bool
		valid   bool
	}{
		{"fresh session is used", t0.Add(3 * time.Hour), false, true},
		{"one expiring within five minutes is not", t0.Add(4 * time.Minute), false, false},
		{"an expired one is not", t0.Add(-time.Minute), false, false},
		{"a corrupt file is not", t0.Add(time.Hour), true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			s := Session{Profile: "lab", CacheDir: dir, Now: func() time.Time { return t0 }}
			writeCache(t, dir, cached{"AKIA1", "secret", "token", tt.expires})
			if tt.corrupt {
				_ = os.WriteFile(filepath.Join(dir, "lab.json"), []byte("{"), 0o600)
			}
			_, ok := s.readCache()
			if ok != tt.valid {
				t.Errorf("readCache ok = %v, want %v", ok, tt.valid)
			}
		})
	}
}

// With a valid cached session, Config uses it as is: no profile lookup and
// no MFA prompt, which is what lets doctor --json and tofu run unattended.
func TestSessionConfigUsesTheCache(t *testing.T) {
	dir := t.TempDir()
	writeCache(t, dir, cached{"AKIA1", "secret", "token", time.Now().Add(time.Hour)})
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "no-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "no-credentials"))
	prompted := false
	s := Session{Profile: "lab", CacheDir: dir, Prompt: func(string) (string, error) { prompted = true; return "", nil }}
	cfg, err := s.Config(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.Env(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if prompted || env["AWS_ACCESS_KEY_ID"] != "AKIA1" || env["AWS_SESSION_TOKEN"] != "token" || env["AWS_REGION"] != Region {
		t.Errorf("prompted %v, env %v", prompted, env)
	}
	s.Forget()
	if _, err := os.Stat(filepath.Join(dir, "lab.json")); !os.IsNotExist(err) {
		t.Error("Forget left the cache file")
	}
}

func TestWriteCacheIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "aws")
	s := Session{Profile: "lab", CacheDir: dir}
	s.writeCache(cached{"AKIA1", "secret", "token", t0})
	fi, err := os.Stat(filepath.Join(dir, "lab.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("cache mode %v, want 0600", fi.Mode().Perm())
	}
}

func TestSyncModule(t *testing.T) {
	mod, work := t.TempDir(), t.TempDir()
	for _, f := range []string{"main.tf", "state.tf", "README.md"} {
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
	// Module .tf files copied, stale .tf removed, generated backend and the
	// local state kept, non-.tf files not copied.
	if want := []string{"backend.tf", "main.tf", "state.tf", "terraform.tfstate"}; !slices.Equal(got, want) {
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
