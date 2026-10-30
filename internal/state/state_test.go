package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		content *string // nil: no file
		wantErr string
		check   func(t *testing.T, s State)
	}{
		{name: "missing file is empty state", check: func(t *testing.T, s State) {
			if s.Version != SchemaVersion || len(s.Stacks) != 0 || s.Stacks == nil {
				t.Errorf("got %+v", s)
			}
		}},
		{name: "valid", content: ptr(`{"version":1,"mode":"local","stacks":{"core":{"status":"applied","updatedAt":"2026-10-30T10:00:00Z"}},"ops":[]}`),
			check: func(t *testing.T, s State) {
				if s.Mode != "local" || !s.IsApplied("core") || s.IsApplied("obs") {
					t.Errorf("got %+v", s)
				}
			}},
		{name: "null stacks becomes empty map", content: ptr(`{"version":1,"stacks":null}`), check: func(t *testing.T, s State) {
			if s.Stacks == nil {
				t.Error("Stacks is nil")
			}
		}},
		{name: "corrupt", content: ptr(`{"version":1,`), wantErr: "unexpected end"},
		{name: "future schema", content: ptr(`{"version":2}`), wantErr: "schema version 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if tt.content != nil {
				if err := os.WriteFile(path, []byte(*tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := NewStore(path).Load()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, s)
		})
	}
}

func TestUpdateRoundTripAndFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".lab") // does not exist yet
	st := NewStore(filepath.Join(dir, "state.json"))
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	err := st.Update(func(s *State) error {
		s.Mode = "local"
		s.Stacks["core"] = StackState{Status: Applied, UpdatedAt: now}
		s.AppendOp(Op{Command: "lab up", Stack: "core", Action: "apply", Status: "ok", Started: now, Finished: now})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != "local" || !got.Stacks["core"].UpdatedAt.Equal(now) || len(got.Ops) != 1 || got.Ops[0].Command != "lab up" {
		t.Errorf("round trip lost data: %+v", got)
	}

	tests := []struct {
		path string
		mode os.FileMode
	}{
		{dir, 0o700 | os.ModeDir},
		{st.Path(), 0o600},
	}
	for _, tt := range tests {
		fi, err := os.Stat(tt.path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode() != tt.mode {
			t.Errorf("%s mode = %v, want %v", tt.path, fi.Mode(), tt.mode)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".state-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestUpdateErrorWritesNothing(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err := st.Update(func(s *State) error { s.Mode = "local"; return nil }); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := st.Update(func(s *State) error { s.Mode = "cloud"; return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if s, _ := st.Load(); s.Mode != "local" {
		t.Errorf("mode = %q after failed update, want local", s.Mode)
	}
}

func TestUpdateOverCorruptFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	err := NewStore(path).Update(func(*State) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("err = %v, called = %v; want an error before fn runs", err, called)
	}
	if b, _ := os.ReadFile(path); string(b) != "{" {
		t.Error("corrupt file was overwritten")
	}
}

// Each goroutine adds one op; the lock must keep all of them.
func TestConcurrentUpdates(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "state.json"))
	const n = 20
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := st.Update(func(s *State) error {
				s.AppendOp(Op{Command: fmt.Sprintf("op %d", i), Action: "apply", Status: "ok"})
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	s, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Ops) != n {
		t.Errorf("ops = %d, want %d (lost updates)", len(s.Ops), n)
	}
}

func TestAppendOpCap(t *testing.T) {
	tests := []struct {
		appends   int
		wantLen   int
		wantFirst string
	}{
		{1, 1, "op 0"},
		{MaxOps, MaxOps, "op 0"},
		{MaxOps + 1, MaxOps, "op 1"},
		{MaxOps + 250, MaxOps, "op 250"},
	}
	for _, tt := range tests {
		s := New()
		for i := range tt.appends {
			s.AppendOp(Op{Command: fmt.Sprintf("op %d", i)})
		}
		if len(s.Ops) != tt.wantLen || s.Ops[0].Command != tt.wantFirst {
			t.Errorf("%d appends: len %d first %q; want %d %q", tt.appends, len(s.Ops), s.Ops[0].Command, tt.wantLen, tt.wantFirst)
		}
	}
}

func ptr(s string) *string { return &s }
