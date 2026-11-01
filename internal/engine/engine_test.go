package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/state"
)

// memStore is an in-memory Store; step logs are kept in memory by path.
type memStore struct {
	st        state.State
	updateErr error
	logs      map[string]*bytes.Buffer
	removed   []string
}

func (m *memStore) OpenStepLog(stack, action string, started time.Time) (io.WriteCloser, string, error) {
	if m.logs == nil {
		m.logs = map[string]*bytes.Buffer{}
	}
	path := fmt.Sprintf("ops/%s-%s-%s.log", started.Format("150405"), action, stack)
	b := &bytes.Buffer{}
	m.logs[path] = b
	return nopCloser{b}, path, nil
}

func (m *memStore) RemoveLogs(ops []state.Op) {
	for _, op := range ops {
		m.removed = append(m.removed, op.Log)
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

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

func (f *fakeRunner) run(action, stack string, log io.Writer) error {
	f.calls = append(f.calls, action+" "+stack)
	_, _ = fmt.Fprintf(log, "runner output for %s\n", stack)
	if f.cancel != nil {
		f.cancel()
	}
	if f.fail[stack] {
		return errors.New("helm timeout")
	}
	return nil
}

func (f *fakeRunner) Apply(_ context.Context, s string, log io.Writer) error {
	return f.run("apply", s, log)
}

func (f *fakeRunner) Destroy(_ context.Context, s string, log io.Writer) error {
	return f.run("destroy", s, log)
}

func TestExecute(t *testing.T) {
	applied := state.Applied
	minimal := map[string]string{
		"cluster": applied, "gitops": applied, "core": applied, "registry": applied, "sample-app": applied,
	}
	withObs := map[string]string{
		"cluster": applied, "gitops": applied, "core": applied, "registry": applied, "sample-app": applied, "obs": applied,
	}
	tests := []struct {
		name       string
		start      state.State
		down       bool
		prune      bool
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
			wantCalls:  []string{"apply registry", "apply cluster", "apply gitops", "apply core", "apply sample-app"},
			wantStacks: minimal,
			wantMode:   "local",
			wantOps:    []string{"apply registry ok", "apply cluster ok", "apply gitops ok", "apply core ok", "apply sample-app ok"},
			wantEvents: []string{"started registry", "finished registry", "started cluster", "finished cluster",
				"started gitops", "finished gitops", "started core", "finished core",
				"started sample-app", "finished sample-app"},
		},
		{
			name:       "kept stacks are skipped",
			start:      stateWith("local", map[string]string{"registry": applied, "cluster": applied, "gitops": applied}),
			wantCalls:  []string{"apply core", "apply sample-app"},
			wantStacks: minimal,
			wantMode:   "local",
			wantOps:    []string{"apply core ok", "apply sample-app ok"},
			wantEvents: []string{"started core", "finished core", "started sample-app", "finished sample-app"},
		},
		{
			name:       "extras outside the profile are left alone without prune",
			start:      stateWith("local", withObs),
			wantStacks: withObs,
			wantMode:   "local",
		},
		{
			name:       "prune destroys extras",
			start:      stateWith("local", withObs),
			prune:      true,
			wantCalls:  []string{"destroy obs"},
			wantStacks: minimal,
			wantMode:   "local",
			wantOps:    []string{"destroy obs ok"},
			wantEvents: []string{"started obs", "finished obs"},
		},
		{
			name:       "failure stops and is recorded",
			start:      state.New(),
			fail:       map[string]bool{"gitops": true},
			wantCalls:  []string{"apply registry", "apply cluster", "apply gitops"},
			wantErr:    "apply gitops: helm timeout",
			wantStacks: map[string]string{"registry": applied, "cluster": applied, "gitops": state.Failed},
			wantMode:   "local",
			wantOps:    []string{"apply registry ok", "apply cluster ok", "apply gitops failed"},
			wantEvents: []string{"started registry", "finished registry", "started cluster", "finished cluster",
				"started gitops", "failed gitops"},
		},
		{
			name:       "down removes stacks and clears mode",
			start:      stateWith("local", map[string]string{"cluster": applied, "gitops": applied}),
			down:       true,
			wantCalls:  []string{"destroy gitops", "destroy cluster"},
			wantStacks: map[string]string{},
			wantMode:   "",
			wantOps:    []string{"destroy gitops ok", "destroy cluster ok"},
			wantEvents: []string{"started gitops", "finished gitops", "started cluster", "finished cluster"},
		},
		{
			name:       "failed destroy keeps the stack as failed",
			start:      stateWith("local", map[string]string{"cluster": applied, "gitops": applied}),
			down:       true,
			fail:       map[string]bool{"gitops": true},
			wantCalls:  []string{"destroy gitops"},
			wantErr:    "destroy gitops: helm timeout",
			wantStacks: map[string]string{"cluster": applied, "gitops": state.Failed},
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
				p, err = eng.PlanDown(cfg, DownOptions{})
			} else {
				p, err = eng.PlanUp(cfg, PlanOptions{Prune: tt.prune})
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
			if err != nil && !strings.Contains(err.Error(), "(log: ops/") {
				t.Errorf("error %q does not point at the step log", err)
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
			for _, op := range store.st.Ops {
				log, ok := store.logs[op.Log]
				if !ok {
					t.Errorf("op %s %s has no log (path %q)", op.Action, op.Stack, op.Log)
					continue
				}
				want := []string{"10:00:00.000 lab up: " + op.Action + " " + op.Stack, "runner output for " + op.Stack}
				if op.Status == "ok" {
					want = append(want, "ok in 0s")
				} else {
					want = append(want, "failed after 0s: helm timeout")
				}
				for _, w := range want {
					if !strings.Contains(log.String(), w) {
						t.Errorf("log %s lacks %q:\n%s", op.Log, w, log.String())
					}
				}
			}
		})
	}
}

func TestExecuteGuards(t *testing.T) {
	g := catalogGraph(t)
	plan := Plan{Mode: "local", Steps: []Step{{"cluster", Apply, ""}, {"gitops", Apply, ""}}}

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

	t.Run("skip and keep steps do not run", func(t *testing.T) {
		store := &memStore{st: state.New()}
		runner := &fakeRunner{}
		p := Plan{Mode: "local", Steps: []Step{{"registry", Keep, ""}, {"secrets", Skip, "not supported"}, {"cluster", Apply, ""}}}
		if err := New(g, store, runner).Execute(context.Background(), p, "lab up", nil); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(runner.calls, []string{"apply cluster"}) {
			t.Errorf("calls = %v, want only apply cluster", runner.calls)
		}
		if _, ok := store.st.Stacks["secrets"]; ok {
			t.Error("a skipped stack was recorded in state")
		}
	})

	t.Run("state write failure is reported", func(t *testing.T) {
		store := &memStore{st: state.New(), updateErr: errors.New("disk full")}
		err := New(g, store, &fakeRunner{}).Execute(context.Background(), plan, "lab up", nil)
		if err == nil || !strings.Contains(err.Error(), "record apply cluster: disk full") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestExecuteRemovesDroppedLogs(t *testing.T) {
	st := state.New()
	for i := range state.MaxOps {
		st.AppendOp(state.Op{Command: "old", Log: fmt.Sprintf("ops/old-%d.log", i)})
	}
	store := &memStore{st: st}
	eng := New(catalogGraph(t), store, &fakeRunner{})
	eng.now = func() time.Time { return time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC) }
	p := Plan{Mode: "local", Steps: []Step{{"registry", Apply, ""}, {"cluster", Apply, ""}}}
	if err := eng.Execute(context.Background(), p, "lab up", nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(store.removed, []string{"ops/old-0.log", "ops/old-1.log"}) {
		t.Errorf("removed = %v, want the two oldest logs", store.removed)
	}
}

func TestRouter(t *testing.T) {
	infra, gitops := &fakeRunner{}, &fakeRunner{fail: map[string]bool{"core": true}}
	r := Router{"registry": infra, "cluster": infra, "gitops": gitops, "core": gitops}
	tests := []struct {
		stack, action string
		wantErr       string
	}{
		{"cluster", "apply", ""},
		{"gitops", "apply", ""},
		{"core", "destroy", "helm timeout"},
		{"obs", "apply", `no runner for stack "obs"`},
		{"obs", "destroy", `no runner for stack "obs"`},
	}
	for _, tt := range tests {
		var err error
		if tt.action == "apply" {
			err = r.Apply(context.Background(), tt.stack, io.Discard)
		} else {
			err = r.Destroy(context.Background(), tt.stack, io.Discard)
		}
		if (tt.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
			t.Errorf("%s %s: err = %v, want %q", tt.action, tt.stack, err, tt.wantErr)
		}
		if got := r.Supports(tt.stack); got != (tt.stack != "obs") {
			t.Errorf("Supports(%s) = %v", tt.stack, got)
		}
	}
	if !slices.Equal(infra.calls, []string{"apply cluster"}) || !slices.Equal(gitops.calls, []string{"apply gitops", "destroy core"}) {
		t.Errorf("infra %v, gitops %v", infra.calls, gitops.calls)
	}
}

func TestTimestampWriter(t *testing.T) {
	clock := time.Date(2026, 10, 30, 23, 5, 1, 250_000_000, time.UTC)
	tests := []struct {
		name   string
		writes []string
		flush  bool
		want   string
	}{
		{"one line", []string{"hello\n"}, false, "23:05:01.250 hello\n"},
		{"line split across writes", []string{"hel", "lo\nwor", "ld\n"}, false, "23:05:01.250 hello\n23:05:01.250 world\n"},
		{"partial line waits for flush", []string{"no newline"}, false, ""},
		{"flush writes the partial line", []string{"no newline"}, true, "23:05:01.250 no newline\n"},
		{"CRLF trimmed", []string{"windows\r\n"}, false, "23:05:01.250 windows\n"},
		{"empty lines kept", []string{"\n"}, false, "23:05:01.250 \n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			w := newTimestampWriter(&b, func() time.Time { return clock })
			for _, s := range tt.writes {
				if n, err := w.Write([]byte(s)); err != nil || n != len(s) {
					t.Fatalf("Write(%q) = %d, %v", s, n, err)
				}
			}
			if tt.flush {
				if err := w.Flush(); err != nil {
					t.Fatal(err)
				}
			}
			if b.String() != tt.want {
				t.Errorf("got %q, want %q", b.String(), tt.want)
			}
		})
	}
}

func TestExecuteRecordsRevision(t *testing.T) {
	tests := []struct {
		name  string
		plan  Plan
		start string
		fail  bool
		want  string
	}{
		{"gitops apply records the plan revision", Plan{Revision: "feat/obs", Steps: []Step{{"gitops", Apply, ""}}}, "", false, "feat/obs"},
		{"failed gitops apply keeps the old revision", Plan{Revision: "main", Steps: []Step{{"gitops", Apply, ""}}}, "feat/obs", true, "feat/obs"},
		{"gitops destroy clears it", Plan{Steps: []Step{{"gitops", Destroy, ""}}}, "main", false, ""},
		{"other stacks leave it alone", Plan{Revision: "main", Steps: []Step{{"core", Apply, ""}}}, "feat/obs", false, "feat/obs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := state.New()
			st.Revision = tt.start
			st.Stacks["gitops"] = state.StackState{Status: state.Applied}
			store := &memStore{st: st}
			r := &fakeRunner{}
			if tt.fail {
				r.fail = map[string]bool{"gitops": true}
			}
			_ = New(catalogGraph(t), store, r).Execute(context.Background(), tt.plan, "lab up", nil)
			if store.st.Revision != tt.want {
				t.Errorf("revision = %q, want %q", store.st.Revision, tt.want)
			}
		})
	}
}
