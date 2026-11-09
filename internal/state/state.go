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
	"sort"
	"syscall"
	"time"
)

// SchemaVersion is written to every state file. Bump it on incompatible changes.
const SchemaVersion = 2

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
	// ActiveLab is the started lab; Labs holds current steps/sessions.
	// Lifetime completion is separate in Progress.
	ActiveLab string                 `json:"activeLab,omitempty"`
	Labs      map[string]LabProgress `json:"labs,omitempty"`
	// ActiveDrill is the drill whose fault is in the cluster, with what it
	// takes to undo it, written before the fault goes in. Drills is the
	// history of finished runs, oldest first.
	ActiveDrill *DrillRun  `json:"activeDrill,omitempty"`
	Drills      []DrillRun `json:"drills,omitempty"`
	Progress    Progress   `json:"progress"`
}

// MaxDrillRuns is how many finished drill runs the history keeps.
const MaxDrillRuns = 200

// Drill run phases, while the run is active.
const (
	DrillInjecting = "injecting" // the fault is going in
	DrillRunning   = "running"   // the fault is in; the timers run
	DrillRestoring = "restoring" // putting everything back
)

// Drill run outcomes, once it ended.
const (
	DrillRecovered    = "recovered"     // verify passed
	DrillAborted      = "aborted"       // lab drills abort
	DrillExpired      = "expired"       // the time limit restored it
	DrillInjectFailed = "inject failed" // the fault never went in fully; restored
	DrillRestoredByUp = "restored by lab up"
)

// DrillRun is one run of a drill.
type DrillRun struct {
	Run    string `json:"run"` // e.g. 20261104T163100Z
	Drill  string `json:"drill"`
	Dir    string `json:"dir"` // the drill's directory, for restore.sh after a crash
	Random bool   `json:"random,omitempty"`
	Phase  string `json:"phase,omitempty"`
	// Started is when the fault was in and the timers started; zero while
	// injecting. Deadline is when the time limit restores the lab.
	Started  time.Time `json:"started,omitzero"`
	Deadline time.Time `json:"deadline"`
	// Alert is the alert the drill expects; AlertFired when Alertmanager
	// first had it firing, if it did before recovery.
	Alert      string    `json:"alert,omitempty"`
	AlertFired time.Time `json:"alertFired,omitzero"`
	// Acked is when an ack named the right cause: it stops the detect
	// timer. Acks are every ack, right or wrong.
	Acked     time.Time  `json:"acked,omitzero"`
	Acks      []DrillAck `json:"acks,omitempty"`
	Recovered time.Time  `json:"recovered,omitzero"` // verify passed
	Hints     int        `json:"hints,omitempty"`
	Verifies  int        `json:"verifies,omitempty"`
	Ended     time.Time  `json:"ended,omitzero"`
	Outcome   string     `json:"outcome,omitempty"`
	Error     string     `json:"error,omitempty"` // the last restore error
	// Timer is the systemd user timer that restores the lab at Deadline.
	Timer string `json:"timer,omitempty"`
	// Test marks a run by a test or a maintainer's check: it never goes
	// into the history (LAB_DRILLS_TEST=1, or the e2e test).
	Test bool `json:"test,omitempty"`
	// Snapshot holds the objects the fault changes, as they were before
	// it; restore puts them back. Kept only while the run is active.
	Snapshot []SavedObject `json:"snapshot,omitempty"`
}

// SavedObject is one Kubernetes object as it was before a drill.
type SavedObject struct {
	APIVersion string `json:"apiVersion"`
	Resource   string `json:"resource"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	// Absent: the object did not exist, so restore deletes it.
	Absent bool            `json:"absent,omitempty"`
	Object json.RawMessage `json:"object,omitempty"`
}

// DrillAck is one lab drills ack: the cause the learner named, and
// whether it was the drill's cause.
type DrillAck struct {
	At    time.Time `json:"at"`
	Cause string    `json:"cause"`
	Right bool      `json:"right,omitempty"`
}

// WrongAcks counts the acks that named the wrong cause.
func (r DrillRun) WrongAcks() int {
	n := 0
	for _, a := range r.Acks {
		if !a.Right {
			n++
		}
	}
	return n
}

// TimeTo is how long after the fault went in t came; zero when either is unknown.
func (r DrillRun) TimeTo(t time.Time) time.Duration {
	if r.Started.IsZero() || t.IsZero() || t.Before(r.Started) {
		return 0
	}
	return t.Sub(r.Started)
}

// EndDrill moves the active drill to the history with outcome, dropping
// its snapshot, and returns the finished run. A test run ends without
// reaching the history.
func (s *State) EndDrill(outcome string, at time.Time) DrillRun {
	if s.ActiveDrill == nil {
		return DrillRun{}
	}
	r := *s.ActiveDrill
	r.Phase, r.Outcome, r.Ended, r.Snapshot, r.Error = "", outcome, at, nil, ""
	s.ActiveDrill = nil
	if r.Test {
		return r
	}
	s.Progress.recordDrill(r)
	s.Drills = append(s.Drills, r)
	if n := len(s.Drills) - MaxDrillRuns; n > 0 {
		s.Drills = append([]DrillRun(nil), s.Drills[n:]...)
	}
	return r
}

// ResetDrills deletes the history of drill id, or of every drill when id
// is "", and returns how many runs it deleted.
func (s *State) ResetDrills(id string) int {
	kept := s.Drills[:0]
	for _, r := range s.Drills {
		if id != "" && r.Drill != id {
			kept = append(kept, r)
		}
	}
	n := len(s.Drills) - len(kept)
	s.Drills = kept
	if id == "" {
		s.Progress.Drills = nil
	} else {
		delete(s.Progress.Drills, id)
	}
	if len(s.Drills) == 0 {
		s.Drills = nil
	}
	return n
}

// LabProgress is how far one lab got.
type LabProgress struct {
	// Step is the step to do next, from 1; one past the last step once the
	// lab is done.
	Step      int         `json:"step"`
	Attempts  map[int]int `json:"attempts,omitempty"`  // verify runs, by step
	Hints     map[int]int `json:"hints,omitempty"`     // hints shown, by step
	Solutions []int       `json:"solutions,omitempty"` // steps whose solution was shown
	Started   time.Time   `json:"started"`
	// Session is when the current session began (zero while stopped);
	// Spent is the time of the sessions before it.
	Session        time.Time     `json:"session,omitzero"`
	Spent          time.Duration `json:"spent"`
	Completed      time.Time     `json:"completed,omitzero"`
	AttemptElapsed time.Duration `json:"attemptElapsedNs,omitempty"`
	AttemptOffset  time.Duration `json:"attemptOffsetNs,omitempty"` // TimeSpent at the last fold/reset
	AttemptKnown   bool          `json:"attemptKnown"`
	AttemptStarted time.Time     `json:"attemptStarted,omitzero"`
}

// TimeSpent is the time of every session, the open one up to now.
func (p LabProgress) TimeSpent(now time.Time) time.Duration {
	if p.Session.IsZero() {
		return p.Spent
	}
	return p.Spent + now.Sub(p.Session)
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
	s := State{Version: SchemaVersion, Stacks: map[string]StackState{}}
	s.Progress.init()
	return s
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

// BilledWarning is what lab status and the TUI header say about a cloud lab
// with a failed stack.
const BilledWarning = "failed, still billed, run lab down"

// HalfBuilt lists, sorted, the failed stacks of a cloud lab. A failed tofu
// apply or destroy can leave AWS resources behind that bill until lab down,
// or the reaper after expires-at, removes them. A local lab returns nil.
func (s State) HalfBuilt() []string {
	if s.Mode != "cloud" {
		return nil
	}
	var failed []string
	for name, ss := range s.Stacks {
		if ss.Status == Failed {
			failed = append(failed, name)
		}
	}
	sort.Strings(failed)
	return failed
}

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

// E2EPath is the drills e2e test's state file for the local lab whose
// state is at local: e2e/state.json beside it (~/.lab/e2e/state.json). Its
// runs are test runs on the main lab, so lab doctor reports an active one
// and lab up restores it, as for the lab's own drills.
func E2EPath(local string) string {
	return filepath.Join(filepath.Dir(local), "e2e", "state.json")
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
	switch s.Version {
	case 1:
		s.migrate()
	case SchemaVersion:
	default:
		return State{}, fmt.Errorf("%s: schema version %d, this lab reads 1 or %d", st.path, s.Version, SchemaVersion)
	}
	s.Progress.init()
	if err := s.validateProgress(); err != nil {
		return State{}, fmt.Errorf("%s: %w", st.path, err)
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
	if err := s.validateProgress(); err != nil {
		return err
	}
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
