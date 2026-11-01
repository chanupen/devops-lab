// Package state persists what the lab has deployed in ~/.lab/state.json,
// along with a log of the operations that changed it.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// SchemaVersion is written to every state file. Bump it on incompatible changes.
const SchemaVersion = 1

// MaxOps is how many operations the log keeps; older ones are dropped.
const MaxOps = 500

// Stack statuses. A destroyed stack is removed from the map.
const (
	Applied = "applied"
	Failed  = "failed"
)

// State is the content of state.json.
type State struct {
	Version int    `json:"version"`
	Mode    string `json:"mode,omitempty"` // set by the first apply; cleared when nothing is left
	// Revision is the Git revision the gitops stack last applied. Empty when
	// gitops is not deployed (or was deployed before revisions were recorded).
	Revision string                `json:"revision,omitempty"`
	Stacks   map[string]StackState `json:"stacks"`
	Ops      []Op                  `json:"ops"`
}

// StackState is the last known result for one stack.
type StackState struct {
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updatedAt"`
	Error     string    `json:"error,omitempty"`
}

// Op is one entry in the operations log.
type Op struct {
	Command  string    `json:"command"` // equivalent CLI command, e.g. "lab up"
	Stack    string    `json:"stack,omitempty"`
	Action   string    `json:"action"`
	Status   string    `json:"status"` // "ok" or "failed"
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Error    string    `json:"error,omitempty"`
	Log      string    `json:"log,omitempty"` // path of the step's output log
}

// New returns an empty state.
func New() State {
	return State{Version: SchemaVersion, Stacks: map[string]StackState{}}
}

// AppendOp adds an op, trims the log to MaxOps and returns the dropped ops.
func (s *State) AppendOp(op Op) []Op {
	s.Ops = append(s.Ops, op)
	n := len(s.Ops) - MaxOps
	if n <= 0 {
		return nil
	}
	dropped := append([]Op(nil), s.Ops[:n]...)
	s.Ops = append([]Op(nil), s.Ops[n:]...)
	return dropped
}

// IsApplied reports whether the stack's last result was a successful apply.
func (s State) IsApplied(stack string) bool { return s.Stacks[stack].Status == Applied }

// DefaultPath returns ~/.lab/state.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lab", "state.json"), nil
}

// PathFor is the state file for a mode. A cloud lab keeps its own state
// (and kubeconfig, expiry, Helm cache) under ~/.lab/cloud, so it can run
// beside a local lab. The lab lock stays on DefaultPath for both, so only
// one lab changes things at a time.
func PathFor(mode string) (string, error) {
	p, err := DefaultPath()
	if err != nil || mode != "cloud" {
		return p, err
	}
	return filepath.Join(filepath.Dir(p), "cloud", "state.json"), nil
}

// Store reads and writes one state file.
type Store struct {
	path string
}

// NewStore returns a store for the file at path. Nothing is created until a write.
func NewStore(path string) *Store { return &Store{path: path} }

// Path returns the state file path.
func (st *Store) Path() string { return st.path }

// Load reads the state. A missing file is an empty state, not an error.
func (st *Store) Load() (State, error) {
	b, err := os.ReadFile(st.path)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, fmt.Errorf("%s: %w", st.path, err)
	}
	if s.Version != SchemaVersion {
		return State{}, fmt.Errorf("%s: schema version %d, this lab reads %d", st.path, s.Version, SchemaVersion)
	}
	if s.Stacks == nil {
		s.Stacks = map[string]StackState{}
	}
	return s, nil
}

// Update loads the state, applies fn and saves the result while holding an
// exclusive lock, so concurrent lab processes do not lose each other's changes.
// If fn returns an error nothing is written.
func (st *Store) Update(fn func(*State) error) error {
	unlock, err := st.lock()
	if err != nil {
		return err
	}
	defer unlock()
	s, err := st.Load()
	if err != nil {
		return err
	}
	if err := fn(&s); err != nil {
		return err
	}
	return st.save(s)
}

// save writes atomically: a temp file in the same directory is synced and
// renamed over the old file, so a crash leaves either the old or the new state.
func (st *Store) save(s State) (err error) {
	dir := filepath.Dir(st.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	s.Version = SchemaVersion
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), st.path); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// OpenStepLog creates ops/<time>-<action>-<stack>.log next to the state
// file, private to the user, and returns it with its path.
func (st *Store) OpenStepLog(stack, action string, started time.Time) (io.WriteCloser, string, error) {
	dir := filepath.Join(filepath.Dir(st.path), "ops")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	name := fmt.Sprintf("%s-%s-%s.log", started.UTC().Format("20060102T150405.000Z"), action, stack)
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, "", err
	}
	return f, path, nil
}

// RemoveLogs deletes the log files of ops dropped from the log. Missing files
// are ignored; only files inside the ops directory are touched.
func (st *Store) RemoveLogs(ops []Op) {
	dir := filepath.Join(filepath.Dir(st.path), "ops")
	for _, op := range ops {
		if op.Log != "" && filepath.Dir(op.Log) == dir {
			_ = os.Remove(op.Log)
		}
	}
}

// ErrBusy is returned by LockLab while another lab process holds the lab.
var ErrBusy = errors.New("lab and its components are already running")

// BusyError says which lab holds the lock.
type BusyError struct {
	Holder string // e.g. "lab up --stacks obs (pid 42 on /dev/pts/3, since 17:20)", if recorded
}

func (e *BusyError) Error() string {
	if e.Holder == "" {
		return ErrBusy.Error()
	}
	return ErrBusy.Error() + ": " + e.Holder
}

// Is makes errors.Is(err, ErrBusy) true.
func (e *BusyError) Is(target error) bool { return target == ErrBusy }

// LockLab claims the lab for this process, so only one lab (the TUI or any
// command) runs at a time. It does not wait: if another lab holds it, it
// returns a *BusyError naming it. holder is recorded for that message. The
// lock is a flock on a sibling .lab.lock file, separate from the state
// file's own lock, which Update takes for each write while the lab runs.
func (st *Store) LockLab(holder string) (unlock func(), err error) {
	return st.tryLock(".lab.lock", holder)
}

// tryLock takes a non-blocking flock on the sibling file with suffix and
// writes holder into it. flock is released when the process exits, so a
// crashed lab never leaves a stale lock.
func (st *Store) tryLock(suffix, holder string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(st.path+suffix, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		held, _ := io.ReadAll(io.LimitReader(f, 512))
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, &BusyError{Holder: string(bytes.TrimSpace(held))}
		}
		return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
	}
	// Best effort: the holder text only improves the busy message.
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(holder+"\n"), 0)
	}
	return func() {
		_ = f.Truncate(0)
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// lock takes an exclusive flock on a sibling .lock file. Linux and macOS only,
// which matches the binaries the Makefile builds.
func (st *Store) lock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(st.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
