package labs

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/state"
)

func TestDurableLabAttempts(t *testing.T) {
	for _, name := range []string{"pause resume", "reset", "failed reset", "scratch cleanup", "unknown uptime", "legacy timing", "reboot gap"} {
		t.Run(name, func(t *testing.T) {
			m, r, clusters, clk := testManager(t)
			id := "02-on-lab"
			if name == "scratch cleanup" || name == "unknown uptime" || name == "reboot gap" {
				id = "01-scratch"
			}
			l, err := m.Find(id)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.Store.Update(func(s *state.State) error {
				for _, stack := range l.Stacks {
					s.Stacks[stack] = state.StackState{Status: state.Applied}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			r.pass["01-verify.sh"], r.pass["02-verify.sh"], r.pass["03-verify.sh"], r.pass["reset.sh"] = true, true, true, true
			if _, err := m.Start(context.Background(), id, io.Discard); err != nil {
				t.Fatal(err)
			}
			if name == "legacy timing" {
				s, err := m.Store.Load()
				if err != nil {
					t.Fatal(err)
				}
				s.Version = 1
				b, err := json.Marshal(s)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(m.Store.Path(), b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			expected := 8 * time.Minute
			if name == "pause resume" {
				clk.t = clk.t.Add(5 * time.Minute)
				if _, _, err := m.Stop(context.Background(), io.Discard); err != nil {
					t.Fatal(err)
				}
				clk.t = clk.t.Add(time.Hour)
				if _, err := m.Start(context.Background(), id, io.Discard); err != nil {
					t.Fatal(err)
				}
				clk.t = clk.t.Add(3 * time.Minute)
				if p := progress(t, m, id); m.TimeSpent(l, p) != 3*time.Minute {
					t.Fatal("displayed session time changed")
				}
			} else {
				clk.t = clk.t.Add(expected)
			}
			if name == "unknown uptime" {
				m.Clusters = nil
			}
			if name == "reboot gap" {
				clusters.up = map[string]Uptime{m.Scratch(l): {Exists: true, Running: true, Started: clk.t.Add(-time.Minute)}}
			}
			for range len(l.Steps) {
				if _, res, err := m.Verify(context.Background(), 0); err != nil || !res.Passed {
					t.Fatalf("%+v %v", res, err)
				}
			}
			if _, _, err := m.Verify(context.Background(), 0); err != nil {
				t.Fatal(err)
			}
			s, err := m.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			b := s.Progress.Labs[id]
			if b.Completions != 1 {
				t.Fatalf("duplicate completion: %+v", b)
			}
			if name == "legacy timing" || name == "unknown uptime" || name == "reboot gap" {
				if b.BestActive != nil {
					t.Fatal("unknown attempt awarded a best")
				}
			} else if b.BestActive == nil || *b.BestActive != expected {
				t.Fatalf("best %+v", b)
			}
			switch name {
			case "scratch cleanup":
				if _, _, err := m.Stop(context.Background(), io.Discard); err != nil {
					t.Fatal(err)
				}
				s, _ = m.Store.Load()
				if s.Labs[id].Step != 0 || s.Progress.Labs[id].Completions != 1 {
					t.Fatal("scratch stop lost historical completion")
				}
				if _, err := m.Start(context.Background(), id, io.Discard); err != nil {
					t.Fatal(err)
				}
				if p := progress(t, m, id); p.Step != 1 || !p.AttemptKnown || p.AttemptElapsed != 0 {
					t.Fatalf("new attempt %+v", p)
				}
			case "reset", "legacy timing", "reboot gap":
				if _, err := m.Reset(context.Background(), io.Discard); err != nil {
					t.Fatal(err)
				}
				clk.t = clk.t.Add(2 * time.Minute)
				for range len(l.Steps) {
					if _, res, err := m.Verify(context.Background(), 0); err != nil || !res.Passed {
						t.Fatalf("%+v %v", res, err)
					}
				}
				s, _ = m.Store.Load()
				b = s.Progress.Labs[id]
				if b.Completions != 2 || b.BestActive == nil || *b.BestActive != 2*time.Minute {
					t.Fatalf("fresh reset %+v", b)
				}
			case "failed reset":
				r.pass["reset.sh"] = false
				if _, err := m.Reset(context.Background(), io.Discard); err == nil {
					t.Fatal("failed reset accepted")
				}
				s, _ = m.Store.Load()
				if s.Progress.Labs[id].Completions != 1 || s.Labs[id].Completed.IsZero() {
					t.Fatal("failed reset changed attempt")
				}
			}
		})
	}
}
