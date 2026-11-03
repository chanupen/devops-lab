package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakePool answers as its user until closed.
type fakePool struct {
	user   string
	mu     sync.Mutex
	closed bool
}

func (p *fakePool) CurrentUser(context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", errors.New("pool closed")
	}
	return p.user, nil
}
func (p *fakePool) Ping(ctx context.Context) error { _, err := p.CurrentUser(ctx); return err }
func (p *fakePool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}

// fakeDB is a database that accepts the logins in valid.
type fakeDB struct {
	valid  map[string]string // user -> password
	opened []*fakePool
}

func (f *fakeDB) connect(_ context.Context, c Creds) (Pool, error) {
	if f.valid[c.User] != c.Password {
		return nil, errors.New("password authentication failed for user " + c.User)
	}
	p := &fakePool{user: c.User}
	f.opened = append(f.opened, p)
	return p, nil
}

func writeCreds(t *testing.T, dir, user, password string) {
	t.Helper()
	for name, v := range map[string]string{"username": user, "password": password + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func testDB(t *testing.T, valid map[string]string) (*DB, *fakeDB, *Metrics, string) {
	t.Helper()
	dir := t.TempDir()
	f := &fakeDB{valid: valid}
	m := newMetrics()
	return newDB(Config{CredDir: dir}, f.connect, m, slog.New(slog.NewTextHandler(io.Discard, nil))), f, m, dir
}

func TestReadCreds(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		want    Creds
		wantErr string
	}{
		{"both, trimmed", map[string]string{"username": "app_v1\n", "password": " p1 \n"}, Creds{"app_v1", "p1"}, ""},
		{"no password file", map[string]string{"username": "app_v1"}, Creds{}, "password"},
		{"empty username", map[string]string{"username": "\n", "password": "p1"}, Creds{}, "username is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for n, v := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, n), []byte(v), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := readCreds(dir)
			if (tt.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) || got != tt.want {
				t.Errorf("readCreds = %+v, %v; want %+v, %q", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

// The rotation: the app moves to new credentials only once they work, and
// never has no pool in between.
func TestRotation(t *testing.T) {
	ctx := context.Background()
	db, f, m, dir := testDB(t, map[string]string{"app_v1": "p1"})
	if _, err := db.Current(); !errors.Is(err, ErrNoPool) {
		t.Fatalf("before the first check: %v", err)
	}
	writeCreds(t, dir, "app_v1", "p1")
	if err := db.Check(ctx); err != nil {
		t.Fatal(err)
	}
	same := func(want string) {
		t.Helper()
		p, err := db.Current()
		if err != nil {
			t.Fatal(err)
		}
		if u, err := p.CurrentUser(ctx); err != nil || u != want {
			t.Errorf("current user = %q, %v; want %s", u, err, want)
		}
	}
	same("app_v1")

	// Unchanged files: no new pool.
	if err := db.Check(ctx); err != nil || len(f.opened) != 1 {
		t.Fatalf("unchanged: %v, %d pools", err, len(f.opened))
	}
	// The Secret moved to app_v2 before the role exists: keep app_v1.
	writeCreds(t, dir, "app_v2", "p2")
	if err := db.Check(ctx); err == nil || !strings.Contains(err.Error(), "connect as app_v2") {
		t.Fatalf("app_v2 before its role exists: %v", err)
	}
	same("app_v1")
	// The role exists now: move, and close the old pool.
	f.valid["app_v2"] = "p2"
	if err := db.Check(ctx); err != nil {
		t.Fatal(err)
	}
	same("app_v2")
	db.wg.Wait()
	if !f.opened[0].closed {
		t.Error("the old pool was not closed")
	}
	if got := testutil.ToFloat64(m.reloads); got != 1 {
		t.Errorf("reloads = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.reconnects.WithLabelValues("failed")); got != 1 {
		t.Errorf("failed reconnects = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.user.WithLabelValues("app_v2")); got != 1 {
		t.Errorf("sampledb_db_user{user=app_v2} = %v", got)
	}
	if n := testutil.CollectAndCount(m.user); n != 1 {
		t.Errorf("%d user series, want only the current one", n)
	}
}

func TestHandler(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		connected bool
		path      string
		code      int
		body      string
	}{
		{"healthz without a database", false, "/healthz", 200, "ok"},
		{"readyz before connecting", false, "/readyz", 503, "not connected"},
		{"check before connecting", false, "/api/check", 503, "not connected"},
		{"readyz", true, "/readyz", 200, "ok"},
		{"check", true, "/api/check", 200, `"user":"app_v1"`},
		{"metrics", true, "/metrics", 200, `sampledb_db_user{user="app_v1"} 1`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, _, m, dir := testDB(t, map[string]string{"app_v1": "p1"})
			if tt.connected {
				writeCreds(t, dir, "app_v1", "p1")
				if err := db.Check(ctx); err != nil {
					t.Fatal(err)
				}
			}
			rec := httptest.NewRecorder()
			newHandler(db, m).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.body) {
				t.Errorf("%s = %d %q, want %d %q", tt.path, rec.Code, rec.Body.String(), tt.code, tt.body)
			}
		})
	}
	// Failed queries are counted.
	db, _, m, _ := testDB(t, nil)
	newHandler(db, m).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/check", nil))
	if got := testutil.ToFloat64(m.dbErrors); got != 1 {
		t.Errorf("db errors = %v, want 1", got)
	}
}
