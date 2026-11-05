package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/gitops"
	labaws "github.com/chanupen/devops-lab/internal/provider/aws"
	"github.com/chanupen/devops-lab/internal/provider/local"
	"github.com/chanupen/devops-lab/internal/state"
)

// labFlags are the flags plan, up and down share.
type labFlags struct {
	cfgPath   string
	statePath string
	mode      string // --mode: overrides lab.yaml's mode
}

// registerMode adds --mode to commands that plan, run or show a lab.
func (f *labFlags) registerMode(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.mode, "mode", "", "local or cloud (default: mode in lab.yaml); each mode keeps its own state")
}

func (f *labFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.cfgPath, "config", "", "path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)")
	cmd.Flags().StringVar(&f.statePath, "state", "", "path to the state file (default ~/.lab/state.json)")
}

// registerTimeout adds --timeout to commands that run stacks.
func registerTimeout(cmd *cobra.Command, d *time.Duration) {
	cmd.Flags().DurationVar(d, "timeout", 30*time.Minute,
		"how long each stack may wait for Argo CD and its apps (image pulls dominate on a slow network)")
}

// configPath is where lab.yaml is: --config when given, else ./lab.yaml,
// else ~/.lab/lab.yaml (a symlink to the repo's file lets ssh -t fedora lab,
// which starts in the home directory, find it). "" when none exists.
func (f *labFlags) configPath() (string, error) {
	if f.cfgPath != "" {
		return f.cfgPath, nil
	}
	candidates := []string{"lab.yaml"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".lab", "lab.yaml"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	return "", nil
}

// loadConfigOnly reads lab.yaml without touching state.
func (f *labFlags) loadConfigOnly() (config.Config, error) {
	p, err := f.configPath()
	if err != nil || p == "" {
		return config.Config{}, errors.Join(err, errors.New("no lab.yaml"))
	}
	return config.Load(p)
}

// load reads the config and returns the state store and stack graph.
func (f *labFlags) load() (config.Config, *state.Store, *engine.Graph, error) {
	cfgPath, err := f.configPath()
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	if cfgPath == "" {
		return config.Config{}, nil, nil, errors.New("no lab.yaml in this directory or ~/.lab; pass --config")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	if f.mode != "" {
		cfg.Mode = f.mode
		if err := cfg.Validate(); err != nil {
			return config.Config{}, nil, nil, fmt.Errorf("--mode %s: %w", f.mode, err)
		}
	}
	path := f.statePath
	if path == "" {
		if path, err = state.PathFor(cfg.Mode); err != nil {
			return config.Config{}, nil, nil, err
		}
	}
	g, err := engine.NewGraph(engine.Catalog())
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	return cfg, state.NewStore(path), g, nil
}

// provider is what lab up and lab down need from a mode's provider.
type provider interface {
	engine.Runner
	Supports(stack string) bool
}

// appAccess is implemented by providers that can list the lab's app URLs.
type appAccess interface {
	AppURLs(ctx context.Context) ([]string, error)
	// AccessHint is one line on how to reach the URLs from the Mac.
	AccessHint() string
}

// labRunner is the default provider: the stack router, plus app URLs built
// from the GitOps runner's Gateway hostnames and the mode's URL format.
type labRunner struct {
	engine.Router
	// checkVersion refuses a Kubernetes pin the mode cannot run; nil when
	// the provider checks at apply time (cloud).
	checkVersion func(context.Context) error
	// checkCluster refuses a running cluster on another version than the
	// pin, or of unknown version; nil in cloud mode.
	checkCluster func(context.Context) error
	hosts        func(context.Context) ([]string, error) // nil without a GitOps runner
	url          func(host string) string
	hint         string
	// start starts the main cluster's and the cache's stopped containers;
	// nil in cloud mode.
	start func(context.Context, io.Writer) ([]local.Unit, error)
	// stale lists deployed stacks to re-apply, with why; nil in cloud mode.
	stale func(context.Context) (map[string]string, error)
}

// staleReporter is implemented by providers that can tell a deployed stack
// no longer matches lab.yaml (lab-zot's host overrides), so lab up
// re-applies it.
type staleReporter interface {
	StaleStacks(ctx context.Context) (map[string]string, error)
}

// StaleStacks implements staleReporter.
func (l labRunner) StaleStacks(ctx context.Context) (map[string]string, error) {
	if l.stale == nil {
		return nil, nil
	}
	return l.stale(ctx)
}

// versionGate is implemented by providers that check the Kubernetes pin
// before lab up does anything.
type versionGate interface {
	CheckK8sVersion(ctx context.Context) error
}

// CheckK8sVersion implements versionGate.
func (l labRunner) CheckK8sVersion(ctx context.Context) error {
	if l.checkVersion == nil {
		return nil
	}
	return l.checkVersion(ctx)
}

// clusterVersionGate is implemented by providers that read the running
// cluster's version and refuse one that differs from the pin. lab up calls
// it after starting stopped containers, since a stopped node cannot be read.
type clusterVersionGate interface {
	CheckClusterVersion(ctx context.Context) error
}

// CheckClusterVersion implements clusterVersionGate.
func (l labRunner) CheckClusterVersion(ctx context.Context) error {
	if l.checkCluster == nil {
		return nil
	}
	return l.checkCluster(ctx)
}

// startStopped starts the lab's stopped containers when the provider can,
// as lab up and lab down do before anything reaches the cluster.
func startStopped(cmd *cobra.Command, prov provider) error {
	s, ok := prov.(stoppedStarter)
	if !ok {
		return nil
	}
	if _, err := s.StartStopped(cmd.Context(), cmd.OutOrStdout()); err != nil {
		return fmt.Errorf("starting the lab's stopped containers: %w", err)
	}
	return nil
}

// stoppedStarter is implemented by providers whose lab can be stopped
// under them, as a reboot stops local containers.
type stoppedStarter interface {
	StartStopped(ctx context.Context, log io.Writer) ([]local.Unit, error)
}

// StartStopped implements stoppedStarter.
func (l labRunner) StartStopped(ctx context.Context, log io.Writer) ([]local.Unit, error) {
	if l.start == nil {
		return nil, nil
	}
	return l.start(ctx, log)
}

// AppURLs implements appAccess.
func (l labRunner) AppURLs(ctx context.Context) ([]string, error) {
	if l.hosts == nil {
		return nil, nil
	}
	hosts, err := l.hosts(ctx)
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(hosts))
	for _, h := range hosts {
		urls = append(urls, l.url(h))
	}
	return urls, nil
}

// AccessHint implements appAccess.
func (l labRunner) AccessHint() string { return l.hint }

// runOptions are the lab up/down flags that reach runners.
type runOptions struct {
	Revision string        // lab up --revision; empty means main
	Purge    bool          // lab down --purge
	Timeout  time.Duration // per-stack wait (Argo CD install, apps healthy)
	RepoRoot string        // the repo (directory of lab.yaml); cloud mode finds its modules there
	// MFAPrompt asks for the MFA code when the cloud session needs one;
	// nil never asks (the cached AWS CLI session must be valid).
	MFAPrompt func(serial string) (string, error)
	// Stacks are the stacks lab up --stacks asks for; they count as active
	// for the compatibility table (activeStacks).
	Stacks []string
}

// activeStacks are the stacks whose compatibility rows limit the
// Kubernetes version: the profile and extraStacks, those asked for, those
// the state file records as applied, and everything they depend on.
func activeStacks(cfg config.Config, home string, asked []string) []string {
	names := append(append([]string{}, cfg.ExtraStacks...), asked...)
	if p, err := engine.ProfileStacks(cfg.Profile); err == nil {
		names = append(names, p...)
	}
	if st, err := state.NewStore(filepath.Join(home, "state.json")).Load(); err == nil {
		for s := range st.Stacks {
			if st.IsApplied(s) {
				names = append(names, s)
			}
		}
	}
	g, err := engine.NewGraph(engine.Catalog())
	if err != nil {
		return nil
	}
	order, err := g.Order(names...)
	if err != nil {
		return nil // an unknown stack: apply every row
	}
	return order
}

// providerFactory builds the runner for cfg.Mode. home is the directory
// holding the state file; generated files (kubeconfig, configs) go under it.
type providerFactory func(cfg config.Config, home string, opts runOptions) (provider, error)

// defaultProvider routes infrastructure stacks to the mode's provider and
// in-cluster stacks to the GitOps runner, which is the same in every mode.
func defaultProvider(cfg config.Config, home string, opts runOptions) (provider, error) {
	kubeconfig := filepath.Join(home, "kubeconfig")
	l := labRunner{Router: engine.Router{}}
	var cloud *labaws.Provider
	var lp *local.Provider
	switch cfg.Mode {
	case "local":
		p, err := local.New(local.Options{Home: home, Kubeconfig: kubeconfig, K8sVersion: cfg.K8sVersion, Purge: opts.Purge,
			RepoRoot: opts.RepoRoot, Stacks: activeStacks(cfg, home, opts.Stacks), ZotHosts: cfg.Zot.AddHosts})
		if err != nil {
			return nil, err
		}
		l.Router["registry"], l.Router["cluster"] = p, p
		l.url, l.hint, l.start, l.checkVersion, l.checkCluster = p.AppURL, p.AccessHint(), p.StartStopped, p.CheckK8sVersion, p.CheckClusterVersion
		l.stale = func(ctx context.Context) (map[string]string, error) {
			why, err := p.ZotStale(ctx)
			if why == "" || err != nil {
				return nil, err
			}
			return map[string]string{"registry": why}, nil
		}
		lp = p
	case "cloud":
		p, err := newCloudProvider(cfg, home, opts)
		if err != nil {
			return nil, err
		}
		l.Router["registry"], l.Router["cluster"] = p, p
		// No load balancer: lab tunnel forwards the same ports a local lab
		// publishes, so the URLs are the same too.
		l.url = local.AppURL
		l.hint = "Run lab tunnel on this host first (it forwards 8080 and 8443 to the cloud Gateway); on the Mac, ssh -N " +
			local.TunnelHost + " as usual."
		cloud = p
	default:
		return nil, fmt.Errorf("%s mode has no provider yet", cfg.Mode)
	}
	if cfg.GitOps == "argocd" && gitops.HasOverlay(cfg.Mode) { // Flux is the second option and not built yet
		g := gitops.New(gitops.Options{Mode: cfg.Mode, Repo: cfg.Repo, Revision: opts.Revision, Timeout: opts.Timeout}, kubeconfig, filepath.Join(home, "helm"))
		for _, s := range gitops.Stacks() {
			l.Router[s] = g
		}
		if lp != nil {
			// Local mode: the secrets stack needs Moto beside the cluster
			// before ESO; sample-db needs its image built and its first
			// credentials in Moto before its app syncs.
			l.Router["secrets"] = seqRunner{lp, g}
			l.Router["sample-db"] = sampleDBRunner{lp, g}
			l.stale = staleWithSampleDB(l.stale, lp, g)
		}
		if cloud != nil {
			// Cloud mode: sample-db's image is built for arm64 and pushed
			// to ECR, and its first credentials go into AWS Secrets
			// Manager, before its app syncs.
			l.Router["sample-db"] = cloudSampleDBRunner{cloud: cloud, gitops: g, repo: opts.RepoRoot}
		}
		l.hosts = g.AppHosts
	}
	if cloud != nil {
		return cloudRunner{labRunner: l, Provider: cloud}, nil
	}
	return l, nil
}

// staleWithSampleDB adds the sample-db stack to what lab up re-applies when
// apps/sample-db no longer matches the image the app-of-apps deploys (the
// app changed in the repo or on a branch). Best effort: when the cluster
// or the registry cannot answer, it adds nothing.
func staleWithSampleDB(next func(context.Context) (map[string]string, error), lp *local.Provider, g *gitops.Runner) func(context.Context) (map[string]string, error) {
	return func(ctx context.Context) (map[string]string, error) {
		out := map[string]string{}
		if next != nil {
			m, err := next(ctx)
			if err != nil {
				return nil, err
			}
			maps.Copy(out, m)
		}
		v, err := g.Value(ctx, "sampleDB")
		if err != nil || v == nil {
			return out, nil
		}
		deployed, _ := v["image"].(string)
		if why, err := lp.SampleDBStale(ctx, deployed); err == nil && why != "" {
			out["sample-db"] = why
		}
		return out, nil
	}
}

// seqRunner applies a stack with first, then second, and destroys it in
// the reverse order: the local provider's part (a container beside the
// cluster), then the GitOps part.
type seqRunner struct{ first, second engine.Runner }

func (r seqRunner) Apply(ctx context.Context, stack string, log io.Writer) error {
	if err := r.first.Apply(ctx, stack, log); err != nil {
		return err
	}
	return r.second.Apply(ctx, stack, log)
}

func (r seqRunner) Destroy(ctx context.Context, stack string, log io.Writer) error {
	if err := r.second.Destroy(ctx, stack, log); err != nil {
		return err
	}
	return r.first.Destroy(ctx, stack, log)
}

// sampleDBRunner applies the sample-db stack in local mode: build and push
// the app's image, make its first credentials in Moto, give the image's
// digest to the app-of-apps, then turn the stack on.
type sampleDBRunner struct {
	local  *local.Provider
	gitops *gitops.Runner
}

func (r sampleDBRunner) Apply(ctx context.Context, stack string, log io.Writer) error {
	ref, err := r.local.BuildSampleDB(ctx, log)
	if err != nil {
		return err
	}
	if err := r.local.SeedSampleDB(ctx, log); err != nil {
		return err
	}
	if err := r.gitops.SetValue(ctx, "sampleDB", map[string]any{"image": ref}, log); err != nil {
		return err
	}
	return r.gitops.Apply(ctx, stack, log)
}

func (r sampleDBRunner) Destroy(ctx context.Context, stack string, log io.Writer) error {
	return r.gitops.Destroy(ctx, stack, log)
}

// cloudSampleDBRunner is sampleDBRunner for cloud mode: the image goes to
// ECR (built by the host's Podman for the arm64 nodes) and the credentials
// to AWS Secrets Manager, with the lab tags. Destroy deletes the secrets
// without a recovery window after the app is gone.
type cloudSampleDBRunner struct {
	cloud  *labaws.Provider
	gitops *gitops.Runner
	repo   string
}

func (r cloudSampleDBRunner) Apply(ctx context.Context, stack string, log io.Writer) error {
	if r.repo == "" {
		return errors.New("the sample-db image is built from the repo: run lab from it, or keep ~/.lab/lab.yaml a symlink into it")
	}
	sock, err := local.PodmanSocket()
	if err != nil {
		return fmt.Errorf("the sample-db image is built with rootless Podman: %w", err)
	}
	dir := filepath.Join(r.repo, "apps", "sample-db")
	tag, err := local.SourceTag(dir)
	if err != nil {
		return fmt.Errorf("sample-db sources: %w", err)
	}
	ref, err := r.cloud.BuildSampleDB(ctx, local.NewPodman(sock), dir, tag, log)
	if err != nil {
		return err
	}
	if err := r.cloud.SeedSampleDB(ctx, log); err != nil {
		return err
	}
	if err := r.gitops.SetValue(ctx, "sampleDB", map[string]any{"image": ref}, log); err != nil {
		return err
	}
	return r.gitops.Apply(ctx, stack, log)
}

func (r cloudSampleDBRunner) Destroy(ctx context.Context, stack string, log io.Writer) error {
	if err := r.gitops.Destroy(ctx, stack, log); err != nil {
		return err
	}
	return r.cloud.RemoveSampleDB(ctx, log)
}

// stackSupport reports which stacks cfg's mode can run, without building a
// provider (which needs Podman or AWS). It matches defaultProvider's routes.
func stackSupport(cfg config.Config) func(string) bool {
	var stacks []string
	switch cfg.Mode {
	case "local":
		stacks = append(stacks, local.Stacks()...)
	case "cloud":
		stacks = append(stacks, labaws.Stacks()...)
	}
	if cfg.GitOps == "argocd" && gitops.HasOverlay(cfg.Mode) {
		stacks = append(stacks, gitops.Stacks()...)
	}
	return func(s string) bool { return slices.Contains(stacks, s) }
}

// unsupported lists stacks the plan would run that the provider cannot. Plans
// made with PlanOptions.Supports skip those already; this guards the rest.
func unsupported(p engine.Plan, prov provider) []string {
	var out []string
	for _, s := range p.Steps {
		if s.Action.Runs() && !prov.Supports(s.Stack) {
			out = append(out, s.Stack)
		}
	}
	return out
}

// withLabLock makes c claim the lab before it runs, so only one lab changes
// it at a time. --help never reaches RunE, so a running lab never refuses it.
func withLabLock(c *cobra.Command) {
	run := c.RunE
	if run == nil {
		return
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		unlock, err := lockLab(cmd)
		if err != nil {
			return err
		}
		defer unlock()
		return run(cmd, args)
	}
}

// lockLab claims the lab for cmd: the state directory from --state when the
// command has it, else ~/.lab. A second lab is refused with what holds it.
func lockLab(cmd *cobra.Command) (func(), error) {
	path := ""
	if f := cmd.Flags().Lookup("state"); f != nil {
		path = f.Value.String()
	}
	if path == "" {
		var err error
		if path, err = state.DefaultPath(); err != nil {
			return nil, err
		}
	}
	unlock, err := state.NewStore(path).LockLab(labHolder(cmd))
	var busy *state.BusyError
	if errors.As(err, &busy) {
		return nil, fmt.Errorf("%w. Only one lab changes things at a time: use that one, or wait for it to finish (q quits the TUI); "+
			"lab status, lab plan and lab doctor still work", busy)
	}
	return unlock, err
}

// labHolder describes this process for a second lab's message, e.g.
// "lab up --stacks obs (pid 42 on /dev/pts/3, since 17:20)".
func labHolder(cmd *cobra.Command) string {
	what := "the TUI"
	if cmd.HasParent() {
		what = cmd.CommandPath()
		cmd.Flags().Visit(func(f *pflag.Flag) {
			if f.Name == "config" || f.Name == "state" {
				return
			}
			what += " --" + f.Name
			if f.Value.Type() != "bool" {
				what += " " + strings.Trim(f.Value.String(), "[]")
			}
		})
	}
	where := fmt.Sprintf("pid %d", os.Getpid())
	if t := terminalName(); t != "" {
		where += " on " + t
	}
	return fmt.Sprintf("%s (%s, since %s)", what, where, time.Now().Format("15:04"))
}

// confirm asks a yes/no question on a terminal. Without a terminal it refuses
// unless yes is set, so scripts must opt in to destructive actions.
func confirm(cmd *cobra.Command, yes bool, question string) error {
	if yes {
		return nil
	}
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok || !isatty.IsTerminal(f.Fd()) {
		return fmt.Errorf("%s: pass --yes to confirm when not running in a terminal", question)
	}
	if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", question); err != nil {
		return err
	}
	answer, _ := bufio.NewReader(f).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	}
	return exitError{code: 1}
}

func homeOf(store *state.Store) string { return filepath.Dir(store.Path()) }
