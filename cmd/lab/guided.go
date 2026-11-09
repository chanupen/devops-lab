package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/doctor"
	"github.com/chanupen/devops-lab/internal/drills"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/state"
)

type guidedDeps struct {
	goos     string
	system   func(string, bool) doctor.System
	checks   func() []doctor.Check
	now      func() time.Time
	lookPath func(string) (string, error)
	confirm  func(*cobra.Command, bool, string) error
	catalog  func(string) ([]labs.Lab, []drills.Drill, error)
}

func hostGuidedDeps() guidedDeps {
	return guidedDeps{
		goos: runtime.GOOS, system: localDoctorSystem, checks: doctor.Checks,
		now: time.Now, lookPath: exec.LookPath, confirm: confirm,
		catalog: func(cfgPath string) ([]labs.Lab, []drills.Drill, error) {
			repo, err := repoRoot(cfgPath)
			if err != nil {
				return nil, nil, err
			}
			ls, err := labs.LoadAll(filepath.Join(repo, "labs"))
			if err != nil {
				return nil, nil, err
			}
			ds, err := drills.LoadAll(filepath.Join(repo, "drills"))
			return ls, ds, err
		},
	}
}

type guidedInput struct {
	cfg            config.Config
	store          *state.Store
	graph          *engine.Graph
	st             state.State
	e2e            state.State
	cfgPath        string
	explicitConfig bool
}

func loadGuided(cmd *cobra.Command, flags labFlags, deps guidedDeps) (guidedInput, error) {
	cfg, store, graph, err := flags.load()
	if err != nil {
		return guidedInput{}, err
	}
	if cfg.Mode != "local" {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "this command is local-only; use a local lab.yaml, not a cloud configuration")
		return guidedInput{}, exitError{code: 2}
	}
	if err := requireLinux(deps.goos, cmd.ErrOrStderr()); err != nil {
		return guidedInput{}, err
	}
	st, err := store.Load()
	if err != nil {
		return guidedInput{}, err
	}
	if st.Mode != "" && st.Mode != "local" {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "this command requires local state")
		return guidedInput{}, exitError{code: 2}
	}
	e2e, err := state.NewStore(state.E2EPath(store.Path())).Load()
	if err != nil {
		return guidedInput{}, err
	}
	cfgPath, err := flags.configPath()
	if err != nil {
		return guidedInput{}, err
	}
	return guidedInput{cfg: cfg, store: store, graph: graph, st: st, e2e: e2e, cfgPath: cfgPath, explicitConfig: flags.cfgPath != ""}, nil
}

func (in guidedInput) activeDrill() (*state.DrillRun, string) {
	if in.st.ActiveDrill != nil {
		return in.st.ActiveDrill, in.store.Path()
	}
	return in.e2e.ActiveDrill, state.E2EPath(in.store.Path())
}

func (in guidedInput) drillReport(now time.Time) *doctor.DrillState {
	r, path := in.activeDrill()
	if r == nil {
		return nil
	}
	label := "a drill"
	if r.Random {
		label = "a random drill"
	}
	detail := r.Error
	if r.Random && detail != "" {
		detail = "recovery did not finish; use the explicit recovery command"
	}
	return &doctor.DrillState{
		Label: label, Phase: r.Phase, Deadline: r.Deadline, Error: detail, Now: now,
		Fix: "lab drills abort --state " + shellArg(path),
	}
}

func selectStartProfile(mem doctor.Memory, explicit string) (string, error) {
	if explicit != "" {
		need, err := doctor.ProfileMemoryBytes(explicit)
		if err != nil {
			return "", err
		}
		if mem.Available < need {
			return explicit, fmt.Errorf("%s requires at least %d GiB available; close memory-heavy apps or select a smaller profile", explicit, need/(1<<30))
		}
		return explicit, nil
	}
	for _, profile := range []string{"full", "standard", "minimal"} {
		need, _ := doctor.ProfileMemoryBytes(profile)
		if mem.Available >= need {
			return profile, nil
		}
	}
	return "minimal", fmt.Errorf("automatic start requires at least 4 GiB available; close memory-heavy apps")
}

func collectGuided(ctx context.Context, in guidedInput, deps guidedDeps, mem doctor.Memory, sys doctor.System, p engine.Plan) []doctor.Result {
	opts := localDoctorOptions(in.cfg, in.st, in.drillReport(deps.now()),
		doctor.Options{Profile: in.cfg.Profile, Memory: &mem, Timeout: 10 * time.Second, RequestedStacks: []string{}})
	for _, step := range p.Steps {
		if step.Action != engine.Skip {
			opts.RequestedStacks = append(opts.RequestedStacks, step.Stack)
		}
	}
	checks := deps.checks()
	for i := range checks {
		run := checks[i].Run
		checks[i].Run = func(ctx context.Context, sys doctor.System, opts doctor.Options) doctor.Result {
			r := run(ctx, sys, opts)
			if err := ctx.Err(); err != nil {
				return doctor.Result{Status: doctor.Fail, Detail: "probe unavailable: " + err.Error(), Unavailable: true}
			}
			return r
		}
	}
	results := doctor.RunObserved(ctx, sys, opts, checks)
	for i := range results {
		if results[i].Unavailable {
			results[i].Status = doctor.Fail
		}
	}
	return results
}

func guidedRuntimeCheck(name string) bool {
	return doctor.RuntimeCheck(name)
}

func guidedRestart(r doctor.Result) bool {
	return doctor.RestartNeeded(r)
}

// Only the displayed copy changes: restarting nodes does not redeploy cached stacks.
func guidedPreview(p engine.Plan, results []doctor.Result) engine.Plan {
	if slices.ContainsFunc(results, func(r doctor.Result) bool {
		return r.Name == "lab cluster" && r.Status == doctor.Stopped
	}) {
		p.Steps = slices.Clone(p.Steps)
		for i := range p.Steps {
			if p.Steps[i].Action == engine.Keep {
				reason := "recorded in state; health unverified until nodes restart"
				if p.Steps[i].Reason != "applied" {
					reason += "; " + p.Steps[i].Reason
				}
				p.Steps[i].Reason = reason
			}
		}
	}
	return p
}

func guidedCommand(name string, in guidedInput, more ...string) string {
	parts := append([]string{"lab", name}, more...)
	if name == "" {
		parts = append([]string{"lab"}, more...)
	}
	// Preserve explicit selection without relying on a later shell's cwd.
	if in.explicitConfig {
		path, err := filepath.Abs(in.cfgPath)
		if err == nil {
			parts = append(parts, "--config", shellArg(path))
		}
	}
	def, _ := state.DefaultPath()
	if in.store.Path() != def {
		path, err := filepath.Abs(in.store.Path())
		if err == nil {
			parts = append(parts, "--state", shellArg(path))
		}
	}
	return strings.Join(parts, " ")
}

func shellArg(s string) string {
	return doctor.ShellArg(s)
}

func eligibleGuidedLab(l labs.Lab, in guidedInput, deps guidedDeps) bool {
	if !slices.Contains(l.Modes, in.cfg.Mode) || !in.st.Labs[l.ID].Completed.IsZero() || in.st.Progress.Labs[l.ID].Completions > 0 {
		return false
	}
	for _, stack := range l.Stacks {
		if !in.st.IsApplied(stack) {
			return false
		}
	}
	for _, tool := range l.Tools {
		if _, err := deps.lookPath(tool); err != nil {
			return false
		}
	}
	return true
}

func sortGuidedLabs(ls []labs.Lab) {
	slices.SortFunc(ls, func(a, b labs.Lab) int {
		an, _ := strconv.Atoi(a.Number())
		bn, _ := strconv.Atoi(b.Number())
		if an != bn {
			return an - bn
		}
		return strings.Compare(a.ID, b.ID)
	})
}
