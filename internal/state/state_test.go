package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
		{name: "future schema", content: ptr(`{"version":3}`), wantErr: "schema version 3"},
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
		appends     int
		wantLen     int
		wantFirst   string
		wantDropped int // by the last append
	}{
		{1, 1, "op 0", 0},
		{MaxOps, MaxOps, "op 0", 0},
		{MaxOps + 1, MaxOps, "op 1", 1},
		{MaxOps + 250, MaxOps, "op 250", 1},
	}
	for _, tt := range tests {
		s := New()
		var dropped []Op
		for i := range tt.appends {
			dropped = s.AppendOp(Op{Command: fmt.Sprintf("op %d", i)})
		}
		if len(dropped) != tt.wantDropped {
			t.Errorf("%d appends: last append dropped %d, want %d", tt.appends, len(dropped), tt.wantDropped)
		}
		if len(s.Ops) != tt.wantLen || s.Ops[0].Command != tt.wantFirst {
			t.Errorf("%d appends: len %d first %q; want %d %q", tt.appends, len(s.Ops), s.Ops[0].Command, tt.wantLen, tt.wantFirst)
		}
	}
}

func ptr(s string) *string { return &s }

func TestStepLogs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".lab")
	st := NewStore(filepath.Join(dir, "state.json"))
	started := time.Date(2026, 10, 30, 17, 42, 15, 123_000_000, time.UTC)
	w, path, err := st.OpenStepLog("cluster", "apply", started)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "ops", "20261030T174215.123Z-apply-cluster.log"); path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
	if _, err := w.Write([]byte("kind: Creating cluster\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		mode os.FileMode
	}{{filepath.Join(dir, "ops"), 0o700 | os.ModeDir}, {path, 0o600}} {
		fi, err := os.Stat(c.path)
		if err != nil || fi.Mode() != c.mode {
			t.Errorf("%s: mode %v, %v; want %v", c.path, fi.Mode(), err, c.mode)
		}
	}

	outside := filepath.Join(t.TempDir(), "keep.log")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		op       Op
		path     string
		wantGone bool
	}{
		{"log in ops dir is removed", Op{Log: path}, path, true},
		{"file outside ops dir is never touched", Op{Log: outside}, outside, false},
		{"missing file is fine", Op{Log: filepath.Join(dir, "ops", "gone.log")}, "", false},
		{"op without log", Op{}, "", false},
	}
	for _, tt := range tests {
		st.RemoveLogs([]Op{tt.op})
		if tt.path == "" {
			continue
		}
		_, err := os.Stat(tt.path)
		if gone := errors.Is(err, os.ErrNotExist); gone != tt.wantGone {
			t.Errorf("%s: gone = %v, want %v", tt.name, gone, tt.wantGone)
		}
	}
}

// A second lab is refused at once with what holds the lab; state updates
// still work while it is held, and it can be claimed again once released.
func TestLockLab(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "state.json"))
	unlock, err := st.LockLab("the TUI (pid 42 on /dev/pts/3, since 17:20)")
	if err != nil {
		t.Fatal(err)
	}
	// Another store on the same file stands in for another process: it opens
	// its own descriptor, which flock treats as a separate holder.
	_, err = NewStore(st.Path()).LockLab("lab up (pid 43)")
	var busy *BusyError
	want := "lab and its components are already running: the TUI (pid 42 on /dev/pts/3, since 17:20)"
	if !errors.As(err, &busy) || !errors.Is(err, ErrBusy) || err.Error() != want {
		t.Fatalf("second lab: %v, want %q", err, want)
	}
	if err := st.Update(func(s *State) error { s.Mode = "local"; return nil }); err != nil {
		t.Fatalf("Update while the lab is held: %v", err)
	}
	unlock()
	unlock2, err := st.LockLab("lab down (pid 44)")
	if err != nil {
		t.Fatalf("LockLab after unlock: %v", err)
	}
	unlock2()
}

func TestHalfBuilt(t *testing.T) {
	stacks := map[string]StackState{"registry": {Status: Applied}, "cluster": {Status: Failed}, "gitops": {Status: Failed}}
	tests := []struct {
		name string
		st   State
		want []string
	}{
		{"cloud lists failed stacks sorted", State{Mode: "cloud", Stacks: stacks}, []string{"cluster", "gitops"}},
		{"local is never billed", State{Mode: "local", Stacks: stacks}, nil},
		{"cloud without failures", State{Mode: "cloud", Stacks: map[string]StackState{"registry": {Status: Applied}}}, nil},
	}
	for _, tt := range tests {
		if got := tt.st.HalfBuilt(); !slices.Equal(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
