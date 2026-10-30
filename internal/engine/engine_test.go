package engine

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/state"
)

// memStore is an in-memory Store.
type memStore struct {
	st        state.State
	updateErr error
}

func (m *memStore) Load() (state.State, error) { return clone(m.st), nil }

func (m *memStore) Update(fn func(*state.State) error) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	s := clone(m.st)
	if err := fn(&s); err != nil {
		return err
	}
	m.st = s
	return nil
}

func clone(s state.State) state.State {
	c := s
	c.Stacks = map[string]state.StackState{}
	for k, v := range s.Stacks {
		c.Stacks[k] = v
	}
	c.Ops = slices.Clone(s.Ops)
	return c
}

// fakeRunner records calls and fails the stacks listed in fail.
type fakeRunner struct {
	calls  []string
	fail   map[string]bool
	cancel context.CancelFunc // called after the first call, if set
}

func (f *fakeRunner) run(action, stack string) error {
	f.calls = append(f.calls, action+" "+stack)
	if f.cancel != nil {
		f.cancel()
	}
	if f.fail[stack] {
		return errors.New("helm timeout")
	}
	return nil
}

func (f *fakeRunner) Apply(_ context.Context, s string) error   { return f.run("apply", s) }
func (f *fakeRunner) Destroy(_ context.Context, s string) error { return f.run("destroy", s) }

func TestExecute(t *testing.T) {
	applied := state.Applied
	tests := []struct {
		name       string
		start      state.State
		down       bool
		fail       map[string]bool
		wantCalls  []string
		wantErr    string
		wantStacks map[string]string // final statuses
		wantMode   string
		wantOps    []string // "action stack status"
		wantEvents []string // "phase stack"
	}{
		{
			name:       "fresh up applies in order",
			start:      state.New(),
			wantCalls:  []string{"apply core", "apply gitops", "apply registry", "apply sample-app"},
			wantStacks: map[string]string{"core": applied, "gitops": applied, "registry": applied, "sample-app": applied},
			wantMode:   "local",
			wantOps:    []string{"apply core ok", "apply gitops ok", "apply registry ok", "apply sample-app ok"},
			wantEvents: []string{"started core", "finished core", "started gitops", "finished gitops",
				"started registry", "finished registry", "started sample-app", "finished sample-app"},
		},
		{
			name:       "kept stacks are skipped",
			start:      stateWith("local", map[string]string{"core": applied, "gitops": applied}),
			wantCalls:  []string{"apply registry", "apply sample-app"},
			wantStacks: map[string]string{"core": applied, "gitops": applied, "registry": applied, "sample-app": applied},
			wantMode:   "local",
			wantOps:    []string{"apply registry ok", "apply sample-app ok"},
			wantEvents: []string{"started registry", "finished registry", "started sample-app", "finished sample-app"},
		},
		{
			name:       "failure stops and is recorded",
			start:      state.New(),
			fail:       map[string]bool{"gitops": true},
			wantCalls:  []string{"apply core", "apply gitops"},
			wantErr:    "apply gitops: helm timeout",
			wantStacks: map[string]string{"core": applied, "gitops": state.Failed},
			wantMode:   "local",
			wantOps:    []string{"apply core ok", "apply gitops failed"},
			wantEvents: []string{"started core", "finished core", "started gitops", "failed gitops"},
		},
		{
			name:       "down removes stacks and clears mode",
			start:      stateWith("local", map[string]string{"core": applied, "gitops": applied}),
			down:       true,
			wantCalls:  []string{"destroy gitops", "destroy core"},
			wantStacks: map[string]string{},
			wantMode:   "",
			wantOps:    []string{"destroy gitops ok", "destroy core ok"},
			wantEvents: []string{"started gitops", "finished gitops", "started core", "finished core"},
		},
		{
			name:       "failed destroy keeps the stack as failed",
			start:      stateWith("local", map[string]string{"core": applied, "gitops": applied}),
			down:       true,
			fail:       map[string]bool{"gitops": true},
			wantCalls:  []string{"destroy gitops"},
			wantErr:    "destroy gitops: helm timeout",
			wantStacks: map[string]string{"core": applied, "gitops": state.Failed},
			wantMode:   "local",
			wantOps:    []string{"destroy gitops failed"},
			wantEvents: []string{"started gitops", "failed gitops"},
		},
	}
	g := catalogGraph(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &memStore{st: tt.start}
			runner := &fakeRunner{fail: tt.fail}
			eng := New(g, store, runner)
			eng.now = func() time.Time { return time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC) }
			cfg := cfgFor("local", "minimal")
			var (
				p   Plan
				err error
			)
			if tt.down {
				p, err = eng.PlanDown(cfg)
			} else {
				p, err = eng.PlanUp(cfg)
			}
			if err != nil {
				t.Fatal(err)
			}
			var events []string
			err = eng.Execute(context.Background(), p, "lab up", func(e Event) {
				events = append(events, string(e.Phase)+" "+e.Step.Stack)
			})
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if !slices.Equal(runner.calls, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", runner.calls, tt.wantCalls)
			}
			if !slices.Equal(events, tt.wantEvents) {
				t.Errorf("events = %v, want %v", events, tt.wantEvents)
			}
			got := map[string]string{}
			for k, v := range store.st.Stacks {
				got[k] = v.Status
			}
			if len(got) != len(tt.wantStacks) {
				t.Errorf("stacks = %v, want %v", got, tt.wantStacks)
			}
			for k, v := range tt.wantStacks {
				if got[k] != v {
					t.Errorf("stack %s = %q, want %q", k, got[k], v)
				}
			}
			if store.st.Mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", store.st.Mode, tt.wantMode)
			}
			var ops []string
			for _, op := range store.st.Ops {
				ops = append(ops, op.Action+" "+op.Stack+" "+op.Status)
				if op.Command != "lab up" || op.Started.IsZero() || op.Finished.IsZero() {
					t.Errorf("op missing fields: %+v", op)
				}
			}
			if !slices.Equal(ops, tt.wantOps) {
				t.Errorf("ops = %v, want %v", ops, tt.wantOps)
			}
		})
	}
}

func TestExecuteGuards(t *testing.T) {
	g := catalogGraph(t)
	plan := Plan{Mode: "local", Steps: []Step{{"core", Apply, ""}, {"gitops", Apply, ""}}}

	t.Run("no runner", func(t *testing.T) {
		err := New(g, &memStore{st: state.New()}, nil).Execute(context.Background(), plan, "lab up", nil)
		if !errors.Is(err, ErrNoRunner) {
			t.Fatalf("err = %v, want ErrNoRunner", err)
		}
	})

	t.Run("cancelled context stops before the next step", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		runner := &fakeRunner{cancel: cancel}
		err := New(g, &memStore{st: state.New()}, runner).Execute(ctx, plan, "lab up", nil)
		if !errors.Is(err, context.Canceled) || len(runner.calls) != 1 {
			t.Fatalf("err = %v, calls = %v; want Canceled after one call", err, runner.calls)
		}
	})

	t.Run("state write failure is reported", func(t *testing.T) {
		store := &memStore{st: state.New(), updateErr: errors.New("disk full")}
		err := New(g, store, &fakeRunner{}).Execute(context.Background(), plan, "lab up", nil)
		if err == nil || !strings.Contains(err.Error(), "record apply core: disk full") {
			t.Fatalf("err = %v", err)
		}
	})
}
