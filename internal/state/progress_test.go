package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestProgressMigration(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.FixedZone("offset", 3600))
	legacy := New()
	legacy.Version, legacy.Mode, legacy.Revision = 1, "local", "main"
	legacy.Stacks["cluster"] = StackState{Status: Failed, Error: "fixture error"}
	legacy.Labs = map[string]LabProgress{
		"retired-lab": {Step: 4, Completed: now, Spent: time.Hour},
		"active-lab":  {Step: 2, Started: now, Session: now, Spent: time.Minute},
	}
	legacy.ActiveLab = "active-lab"
	legacy.ActiveDrill = &DrillRun{Run: "active", Drill: "retired-drill", Phase: DrillRestoring, Error: "restore failed",
		Snapshot: []SavedObject{{Resource: "deployments", Object: json.RawMessage(`{"metadata":{"name":"sample"}}`)}}}
	legacy.Drills = []DrillRun{
		{Drill: "retired-drill", Started: now, Acked: now.Add(time.Minute), Recovered: now.Add(2 * time.Minute), Outcome: DrillRecovered},
		{Drill: "retired-drill", Outcome: DrillAborted},
		{Drill: "test", Test: true, Outcome: DrillRecovered},
	}
	legacy.AppendOp(Op{Command: "fixture"})
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &legacy); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			input := b
			if version == 2 {
				converted := legacy
				converted.Labs = make(map[string]LabProgress)
				for id, p := range legacy.Labs {
					converted.Labs[id] = p
				}
				converted.migrate()
				input, err = json.Marshal(converted)
				if err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(path, input, 0o600); err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(path)
			store := NewStore(path)
			got, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			again, err := store.Load()
			if err != nil || !reflect.DeepEqual(got, again) {
				t.Fatalf("not idempotent: %v", err)
			}
			after, _ := os.ReadFile(path)
			afterInfo, _ := os.Stat(path)
			if !bytes.Equal(input, after) || !info.ModTime().Equal(afterInfo.ModTime()) {
				t.Fatal("load wrote migration")
			}
			if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
				t.Fatal("read acquired a lock")
			}
			lab, drill := got.Progress.Labs["retired-lab"], got.Progress.Drills["retired-drill"]
			if got.Version != 2 || lab.Completions != 1 || lab.BestActive != nil || !lab.LastCompleted.Equal(now) {
				t.Fatalf("lab %+v", lab)
			}
			if drill.Runs != 2 || drill.Recovered != 1 || drill.BestRecovery == nil || *drill.BestRecovery != 2*time.Minute {
				t.Fatalf("drill %+v", drill)
			}
			if got.Labs["active-lab"].AttemptKnown || !reflect.DeepEqual(got.ActiveDrill, legacy.ActiveDrill) ||
				!reflect.DeepEqual(got.Drills, legacy.Drills) || !reflect.DeepEqual(got.Ops, legacy.Ops) ||
				!reflect.DeepEqual(got.Stacks, legacy.Stacks) || got.Revision != legacy.Revision || got.Mode != legacy.Mode || got.ActiveLab != legacy.ActiveLab {
				t.Fatal("legacy recovery/session data changed")
			}
			boom := errors.New("failed transaction")
			if err := store.Update(func(s *State) error { s.RecordLabCompletion("retired-lab", now, time.Minute, true); return boom }); !errors.Is(err, boom) {
				t.Fatal(err)
			}
			after, _ = os.ReadFile(path)
			if !bytes.Equal(input, after) {
				t.Fatal("failed transaction persisted migration")
			}
			if err := store.Update(func(s *State) error { s.Revision = "fixture-update"; return nil }); err != nil {
				t.Fatal(err)
			}
			persisted, err := store.Load()
			if err != nil || persisted.Version != 2 || persisted.Progress.Labs["retired-lab"].Completions != 1 {
				t.Fatalf("%+v %v", persisted, err)
			}
		})
	}
}

func TestProgressRejectsInvalidState(t *testing.T) {
	for _, content := range []string{
		`{`, `{}`, `null`, `{"version":0}`, `{"version":3}`,
		`{"version":2,"progress":{"labs":{"x":{"completions":-1}}}}`,
		`{"version":2,"progress":{"labs":{"x":{"bestActiveNs":0}}}}`,
		`{"version":2,"progress":{"drills":{"x":{"runs":1,"recovered":2}}}}`,
		`{"version":2,"progress":{"drills":{"x":{"bestRecoveryNs":-1}}}}`,
		`{"version":2,"labs":{"x":{"attemptElapsedNs":-1}}}`,
	} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			store := NewStore(path)
			if _, err := store.Load(); err == nil {
				t.Fatal("invalid state accepted")
			}
			called := false
			if err := store.Update(func(*State) error { called = true; return nil }); err == nil || called {
				t.Fatal("invalid state mutated")
			}
			after, _ := os.ReadFile(path)
			if string(after) != content {
				t.Fatal("invalid state replaced")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "missing", "state.json")
	if s, err := NewStore(path).Load(); err != nil || s.Version != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("read created directory")
	}
}

func TestDurableDrillOutcomes(t *testing.T) {
	for _, tt := range []struct {
		name, outcome     string
		test, invalidTime bool
		recovered         int
	}{
		{"recovered", DrillRecovered, false, false, 1},
		{"aborted", DrillAborted, false, false, 0},
		{"expired", DrillExpired, false, false, 0},
		{"inject failed", DrillInjectFailed, false, false, 0},
		{"restored by up", DrillRestoredByUp, false, false, 0},
		{"test recovered", DrillRecovered, true, false, 0},
		{"invalid time", DrillRecovered, false, true, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
			at := start.Add(time.Minute)
			if tt.invalidTime {
				at = start.Add(-time.Minute)
			}
			s.ActiveDrill = &DrillRun{Drill: "fixture", Started: start, AlertFired: at, Acked: at, Recovered: at, Test: tt.test}
			s.EndDrill(tt.outcome, at)
			b := s.Progress.Drills["fixture"]
			if tt.test {
				if len(s.Progress.Drills) != 0 || len(s.Drills) != 0 {
					t.Fatal("test run counted")
				}
				return
			}
			if b.Runs != 1 || b.Recovered != tt.recovered {
				t.Fatalf("summary %+v", b)
			}
			if tt.recovered == 0 || tt.invalidTime {
				if b.BestAlert != nil || b.BestDetect != nil || b.BestRecovery != nil {
					t.Fatal("invalid/non-recovery best")
				}
			} else if b.BestRecovery == nil || *b.BestRecovery != time.Minute {
				t.Fatalf("best %+v", b)
			}
		})
	}
}

func TestDurableHistoryTrimmingAndReset(t *testing.T) {
	s := New()
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := range MaxDrillRuns + 5 {
		d := time.Second
		if i > 0 {
			d = time.Minute
		}
		s.ActiveDrill = &DrillRun{Drill: "dns", Started: start, Recovered: start.Add(d)}
		s.EndDrill(DrillRecovered, start.Add(d+time.Second))
	}
	b := s.Progress.Drills["dns"]
	if len(s.Drills) != MaxDrillRuns || b.Runs != MaxDrillRuns+5 || b.Recovered != MaxDrillRuns+5 || *b.BestRecovery != time.Second {
		t.Fatalf("trimmed best: %+v", b)
	}
	s.ActiveDrill = &DrillRun{Drill: "dns", Test: true}
	s.EndDrill(DrillRecovered, start)
	if !reflect.DeepEqual(b, s.Progress.Drills["dns"]) {
		t.Fatal("test run counted")
	}
	s.ActiveDrill = &DrillRun{Drill: "other"}
	s.EndDrill(DrillAborted, start)
	if n := s.ResetDrills("dns"); n != MaxDrillRuns-1 || s.Progress.Drills["dns"].Runs != 0 || s.Progress.Drills["other"].Runs != 1 {
		t.Fatal("targeted reset")
	}
	s.ResetDrills("")
	if len(s.Progress.Drills) != 0 || len(s.Drills) != 0 {
		t.Fatal("global reset")
	}
}

func TestProgressAtomicFailureAndConcurrentCompletions(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err := store.Update(func(s *State) error {
		s.ActiveDrill = &DrillRun{Run: "saved", Snapshot: []SavedObject{{Object: json.RawMessage(`{}`)}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(store.Path())
	err := store.Update(func(s *State) error {
		s.EndDrill(DrillRecovered, time.Now())
		s.ActiveDrill = &DrillRun{Snapshot: []SavedObject{{Object: json.RawMessage(`bad`)}}}
		return nil
	})
	if err == nil {
		t.Fatal("invalid snapshot saved")
	}
	after, _ := os.ReadFile(store.Path())
	if !bytes.Equal(before, after) {
		t.Fatal("failed save lost active snapshot")
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.Update(func(s *State) error { s.RecordLabCompletion("lab", time.Now(), time.Minute, true); return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := store.Load()
	if err != nil || got.Progress.Labs["lab"].Completions != 12 {
		t.Fatalf("%+v %v", got.Progress, err)
	}
}
