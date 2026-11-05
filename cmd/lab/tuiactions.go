package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/state"
	"github.com/chanupen/devops-lab/internal/tui"
)

// tuiActions runs the TUI's operations through the same plan, lock, engine
// and providers as lab up and lab down, and its reports through the CLI
// commands themselves.
type tuiActions struct {
	cfg         config.Config
	cfgPath     string // "" when the defaults apply
	store       *state.Store
	newProvider providerFactory
	timeout     time.Duration
}

func (a tuiActions) graph() (*engine.Graph, error) { return engine.NewGraph(engine.Catalog()) }

// command is the CLI command for req, built the way lab up and lab down
// build theirs, so the ops log reads the same from either.
func command(req tui.Request) string {
	if req.Down {
		return downCommand(req.Stacks, false)
	}
	return upCommand(req.Stacks, false, "", false)
}

// Plan implements tui.Actions.
func (a tuiActions) Plan(_ context.Context, req tui.Request) (engine.Plan, error) {
	g, err := a.graph()
	if err != nil {
		return engine.Plan{}, err
	}
	eng := engine.New(g, a.store, nil)
	if req.Down {
		return eng.PlanDown(a.cfg, downOptions(a.cfg.Mode, req.Stacks))
	}
	return eng.PlanUp(a.cfg, engine.PlanOptions{Stacks: req.Stacks, Supports: stackSupport(a.cfg)})
}

// Run implements tui.Actions. It prints what lab up or lab down would, as
// lines, plus each step's log as it is written.
func (a tuiActions) Run(ctx context.Context, req tui.Request, progress func(tui.Progress)) (engine.Plan, error) {
	// Only a TUI that holds the lab lock offers actions, and it runs one at
	// a time, so there is nothing more to claim here.
	cmd := command(req)
	g, err := a.graph()
	if err != nil {
		return engine.Plan{}, err
	}
	out := &lineWriter{emit: func(l string) { progress(tui.Progress{Line: l}) }}
	defer out.Flush()
	prov, err := a.newProvider(a.cfg, homeOf(a.store), runOptions{Timeout: a.timeout})
	if err != nil {
		return engine.Plan{}, err
	}
	eng := engine.New(g, a.store, nil)
	var p engine.Plan
	if req.Down {
		p, err = eng.PlanDown(a.cfg, downOptions(a.cfg.Mode, req.Stacks))
	} else {
		p, err = eng.PlanUp(a.cfg, engine.PlanOptions{Stacks: req.Stacks, Supports: prov.Supports})
	}
	if err != nil {
		return engine.Plan{}, err
	}
	if err := writePlan(out, lipgloss.NewRenderer(io.Discard), p); err != nil {
		return p, err
	}
	if p.Changes() == 0 {
		_, _ = io.WriteString(out, "\nnothing to do\n")
		return p, nil
	}
	if missing := unsupported(p, prov); len(missing) > 0 {
		return p, fmt.Errorf("the %s provider cannot run %s yet", p.Mode, strings.Join(missing, ", "))
	}
	if err := prefetchCharts(ctx, prov, p, out); err != nil {
		return p, err
	}
	_, _ = fmt.Fprintf(out, "\n$ %s\n", cmd)
	store := teeStore{Store: a.store, tee: out}
	err = engine.New(g, store, prov).Execute(ctx, p, cmd, func(e engine.Event) {
		progress(tui.Progress{Event: &e})
	})
	return p, err
}

// Report implements tui.Actions by running the read-only CLI command
// in-process. It needs no lock, so read-only TUIs can run it too.
func (a tuiActions) Report(ctx context.Context, name string) (string, error) {
	args := []string{name}
	if name != "doctor" {
		if a.cfgPath != "" {
			args = append(args, "--config", a.cfgPath)
		}
		args = append(args, "--state", a.store.Path())
	}
	root := newRootCmdWith(a.newProvider) // status, plan and doctor take no lock
	var b bytes.Buffer
	root.SetOut(&b)
	root.SetErr(&b)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	return b.String(), err
}

// teeStore copies every step log to tee as well as to its file.
type teeStore struct {
	*state.Store
	tee io.Writer
}

// OpenStepLog implements engine.Store.
func (t teeStore) OpenStepLog(stack, action string, started time.Time) (io.WriteCloser, string, error) {
	w, path, err := t.Store.OpenStepLog(stack, action, started)
	if err != nil {
		return nil, "", err
	}
	return struct {
		io.Writer
		io.Closer
	}{io.MultiWriter(w, t.tee), w}, path, nil
}

// lineWriter calls emit for each complete line written to it. Steps may
// write from several goroutines.
type lineWriter struct {
	mu   sync.Mutex
	buf  []byte
	emit func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.emit(strings.TrimRight(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
}

// Flush emits a trailing partial line.
func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit(string(w.buf))
		w.buf = nil
	}
}

// branchExists asks the remote whether a branch still exists, with the
// deploy key when lab.yaml names one. It never prompts and never changes
// known_hosts.
func branchExists(repo config.Repo) func(ctx context.Context, branch string) (bool, error) {
	return func(ctx context.Context, branch string) (bool, error) {
		if repo.URL == "" {
			return false, errors.New("no repo.url in lab.yaml")
		}
		c := exec.CommandContext(ctx, "git", "ls-remote", "--heads", "--exit-code", repo.URL, "refs/heads/"+branch)
		sshCmd := "ssh -o BatchMode=yes"
		if repo.SSHKeyFile != "" {
			key, err := expandHome(repo.SSHKeyFile)
			if err != nil {
				return false, err
			}
			sshCmd += " -o IdentitiesOnly=yes -i " + key
		}
		c.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd, "GIT_TERMINAL_PROMPT=0")
		err := c.Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
			return true, nil
		case errors.As(err, &exit) && exit.ExitCode() == 2: // --exit-code: no matching ref
			return false, nil
		default:
			return false, fmt.Errorf("git ls-remote: %w", err)
		}
	}
}

func expandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home + p[1:], nil
}
