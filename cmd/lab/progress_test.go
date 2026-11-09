package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/drills"
	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/state"
	"github.com/chanupen/devops-lab/internal/tui"
)

func TestProgressAliasesReadOnly(t *testing.T) {
	fakePodmanSocket(t, map[string]fakeContainer{})
	cfg, err := filepath.Abs("../../lab.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"labs", "drills"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			legacy := []byte(`{"version":1,"labs":{"01-argocd-bootstrap":{"completed":"2026-11-01T10:00:00Z"}}}`)
			if err := os.WriteFile(path, legacy, 0o600); err != nil {
				t.Fatal(err)
			}
			store := state.NewStore(path)
			unlock, err := store.LockLab("fixture holder")
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			info, _ := os.Stat(path)
			flags := []string{"--config", cfg, "--state", path}
			bare, err := runLab(append([]string{name}, flags...)...)
			if err != nil {
				t.Fatal(err)
			}
			list, err := runLab(append([]string{name, "list"}, flags...)...)
			if err != nil {
				t.Fatal(err)
			}
			if bare != list || !strings.HasPrefix(bare, "Progress: ") {
				t.Fatalf("alias != list:\n%s\n%s", bare, list)
			}
			help, err := runLab(name, "--help")
			if err != nil || !strings.Contains(help, "Available Commands:") {
				t.Fatalf("help %s %v", help, err)
			}
			after, _ := os.ReadFile(path)
			afterInfo, _ := os.Stat(path)
			if !bytes.Equal(legacy, after) || !info.ModTime().Equal(afterInfo.ModTime()) {
				t.Fatal("list wrote migration")
			}
		})
	}
}

func TestProgressViewGoldens(t *testing.T) {
	allLabs := []labs.Lab{{ID: "01-first", Title: "First", Modes: []string{"local"}, Needs: labs.NeedsScratch, Steps: []labs.Step{{}}},
		{ID: "02-cloud", Title: "Cloud", Modes: []string{"cloud"}, Needs: labs.NeedsLab}}
	allDrills := []drills.Drill{{ID: "incident", Title: "Incident", Stacks: []string{"core"}}}
	for _, name := range []string{"empty", "completed", "unknown-best", "cloud"} {
		t.Run(name, func(t *testing.T) {
			st := state.New()
			cfg := config.Default()
			ds := allDrills
			if name != "empty" {
				st.RecordLabCompletion("01-first", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 18*time.Minute, name != "unknown-best")
				st.ActiveDrill = &state.DrillRun{Drill: "incident", Started: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
				st.ActiveDrill.Recovered = st.ActiveDrill.Started.Add(4 * time.Minute)
				st.ActiveDrill.Acked = st.ActiveDrill.Started.Add(time.Minute)
				st.EndDrill(state.DrillRecovered, st.ActiveDrill.Recovered)
				st.Drills = nil // lifetime summary survives absent raw history
			}
			if name == "cloud" {
				cfg.Mode = "cloud"
				ds = nil
				st.RecordLabCompletion("02-cloud", time.Now(), 0, false)
			}
			var out bytes.Buffer
			if err := writeLabList(&out, allLabs, st, func(labs.Lab, state.LabProgress) time.Duration { return 0 }, cfg.Mode); err != nil {
				t.Fatal(err)
			}
			if cfg.Mode == "local" {
				if err := writeDrillList(&out, allDrills, st); err != nil {
					t.Fatal(err)
				}
				if err := writeDrillHistory(&out, st); err != nil {
					t.Fatal(err)
				}
			}
			model := tui.New(tui.Options{Config: cfg, State: st, Labs: allLabs, DrillList: ds, ReadOnly: "fixture holder"})
			view := model.View()
			wantCounts := st.Progress.Counts(labs.CatalogIDs(allLabs, cfg.Mode), drills.CatalogIDs(ds)).String()
			if !strings.Contains(view, wantCounts) {
				t.Fatalf("Home doesn't match CLI counts: %s", view)
			}
			path := filepath.Join("testdata", "progress", name+".golden")
			if os.Getenv("UPDATE_PROGRESS_GOLDENS") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if out.String() != string(want) {
				t.Fatalf("golden mismatch:\n%s", out.String())
			}
		})
	}
}
