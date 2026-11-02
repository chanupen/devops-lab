package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/reaper"
)

var now = time.Date(2026, 11, 2, 11, 15, 0, 0, time.UTC)

type lister []reaper.Resource

func (l lister) List(context.Context) ([]reaper.Resource, error) { return l, nil }

type deleter struct{}

func (deleter) Delete(context.Context, reaper.Target) error { return nil }

type teardown struct {
	starts   int
	startErr error
	checkErr error
}

func (t *teardown) Running(context.Context) (string, error)           { return "", nil }
func (t *teardown) Get(context.Context, string) (reaper.Build, error) { return reaper.Build{}, nil }
func (t *teardown) Check(context.Context) error                       { return t.checkErr }
func (t *teardown) Start(context.Context, int, int) (string, error) {
	t.starts++
	if t.startErr != nil {
		return "", t.startErr
	}
	return fmt.Sprintf("lab-teardown:%d", t.starts), nil
}

type store struct{ loadErr error }

func (s store) Load(context.Context) (reaper.TeardownState, error) {
	return reaper.TeardownState{}, s.loadErr
}
func (store) Save(context.Context, reaper.TeardownState) error { return nil }
func (store) Clear(context.Context) error                      { return nil }

func expiredCluster() reaper.Resource {
	return reaper.Resource{ARN: "arn:aws:eks:ap-south-1:111122223333:cluster/lab",
		Tags: map[string]string{"lab": "true", "component": "cluster", "expires-at": now.Add(-time.Hour).Format(time.RFC3339)}}
}

// The failure-destination path: a scheduled run that is not clean returns
// an error, which is what makes Lambda send the async invocation to its
// on-failure destination (SNS lab-teardown, an email). It still writes an
// unclean heartbeat, so lab doctor and lab up --mode cloud see why.
func TestInvokeFailsWhenNotClean(t *testing.T) {
	denied := errors.New("api error AccessDenied: not authorized to perform: s3:ListBucket")
	tests := []struct {
		name     string
		env      reaper.Env
		ev       Event
		wantErr  string // "" for a clean run
		starts   int
		beats    int
		selfTest bool // the heartbeat carries a passing self-test
	}{
		{
			name:  "clean: nothing expired, self-test passes",
			env:   reaper.Env{Lister: lister{}, Deleter: deleter{}, Teardown: &teardown{}, Store: store{}},
			beats: 1, selfTest: true,
		},
		{
			name:    "the incident: state unreadable, the build starts anyway and the run fails",
			env:     reaper.Env{Lister: lister{expiredCluster()}, Deleter: deleter{}, Teardown: &teardown{}, Store: store{loadErr: denied}},
			wantErr: "s3:ListBucket", starts: 1, beats: 1,
		},
		{
			name:    "StartBuild refused",
			env:     reaper.Env{Lister: lister{expiredCluster()}, Deleter: deleter{}, Teardown: &teardown{startErr: errors.New("AccessDeniedException")}, Store: store{}},
			wantErr: "AccessDeniedException", starts: 1, beats: 1, selfTest: true,
		},
		{
			name:    "self-test fails with nothing expired: still an error, so it emails",
			env:     reaper.Env{Lister: lister{}, Deleter: deleter{}, Teardown: &teardown{checkErr: errors.New("CodeBuild project lab-teardown not found")}, Store: store{}},
			wantErr: "self-test: teardown project", beats: 1,
		},
		{
			name: "sweep-only calls from the build write no heartbeat and skip the self-test",
			env:  reaper.Env{Lister: lister{}, Deleter: deleter{}},
			ev:   Event{SweepOnly: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var beats []reaper.Heartbeat
			beat := func(_ context.Context, b []byte) error {
				var hb reaper.Heartbeat
				if err := json.Unmarshal(b, &hb); err != nil {
					t.Fatal(err)
				}
				beats = append(beats, hb)
				return nil
			}
			_, err := invoke(context.Background(), tt.env, tt.ev, reaper.Options{SweepOnly: tt.ev.SweepOnly}, now, beat, func(string, ...any) {})
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("err = %v, want a clean run", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want one containing %q (no error means no failure email)", err, tt.wantErr)
			}
			if td, ok := tt.env.Teardown.(*teardown); ok && td.starts != tt.starts {
				t.Errorf("StartBuild called %d times, want %d", td.starts, tt.starts)
			}
			if len(beats) != tt.beats {
				t.Fatalf("%d heartbeats, want %d", len(beats), tt.beats)
			}
			if tt.beats == 0 {
				return
			}
			hb := beats[0]
			if hb.Clean != (tt.wantErr == "") || (len(hb.Errors) == 0) != hb.Clean {
				t.Errorf("heartbeat clean %v with errors %q", hb.Clean, hb.Errors)
			}
			if hb.SelfTest == nil || hb.SelfTest.OK() != tt.selfTest {
				t.Errorf("heartbeat self-test %+v, want OK %v", hb.SelfTest, tt.selfTest)
			}
		})
	}
}

// A heartbeat that cannot be written is an error too: otherwise the run
// looks fine while lab up --mode cloud refuses for a stale heartbeat.
func TestInvokeHeartbeatWriteFails(t *testing.T) {
	env := reaper.Env{Lister: lister{}, Deleter: deleter{}, Teardown: &teardown{}, Store: store{}}
	_, err := invoke(context.Background(), env, Event{}, reaper.Options{}, now,
		func(context.Context, []byte) error { return errors.New("AccessDenied") }, func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "write heartbeat") {
		t.Errorf("err = %v", err)
	}
}
