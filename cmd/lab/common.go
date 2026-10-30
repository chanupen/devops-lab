package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/gitops"
	"github.com/chanupen/devops-lab/internal/provider/local"
	"github.com/chanupen/devops-lab/internal/state"
)

// labFlags are the flags plan, up and down share.
type labFlags struct {
	cfgPath   string
	statePath string
}

func (f *labFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.cfgPath, "config", "lab.yaml", "path to lab.yaml")
	cmd.Flags().StringVar(&f.statePath, "state", "", "path to the state file (default ~/.lab/state.json)")
}

// registerTimeout adds --timeout to commands that run stacks.
func registerTimeout(cmd *cobra.Command, d *time.Duration) {
	cmd.Flags().DurationVar(d, "timeout", 30*time.Minute,
		"how long each stack may wait for Argo CD and its apps (image pulls dominate on a slow network)")
}

// load reads the config and returns the state store and stack graph.
func (f *labFlags) load() (config.Config, *state.Store, *engine.Graph, error) {
	cfg, err := config.Load(f.cfgPath)
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	path := f.statePath
	if path == "" {
		if path, err = state.DefaultPath(); err != nil {
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

// runOptions are the lab up/down flags that reach runners.
type runOptions struct {
	Purge   bool          // lab down --purge
	Timeout time.Duration // per-stack wait (Argo CD install, apps healthy)
}

// providerFactory builds the runner for cfg.Mode. home is the directory
// holding the state file; generated files (kubeconfig, configs) go under it.
type providerFactory func(cfg config.Config, home string, opts runOptions) (provider, error)

// defaultProvider routes infrastructure stacks to the mode's provider and
// in-cluster stacks to the GitOps runner, which is the same in every mode.
func defaultProvider(cfg config.Config, home string, opts runOptions) (provider, error) {
	kubeconfig := filepath.Join(home, "kubeconfig")
	r := engine.Router{}
	switch cfg.Mode {
	case "local":
		p, err := local.New(local.Options{Home: home, Kubeconfig: kubeconfig, K8sVersion: cfg.K8sVersion, Purge: opts.Purge})
		if err != nil {
			return nil, err
		}
		r["registry"], r["cluster"] = p, p
	default:
		return nil, fmt.Errorf("%s mode has no provider yet", cfg.Mode)
	}
	if cfg.GitOps == "argocd" { // Flux is the second option and not built yet
		g := gitops.New(gitops.Options{Mode: cfg.Mode, Repo: cfg.Repo, Timeout: opts.Timeout}, kubeconfig, filepath.Join(home, "helm"))
		r["gitops"], r["core"] = g, g
	}
	return r, nil
}

// unsupported lists stacks the plan would change that the provider cannot run.
func unsupported(p engine.Plan, prov provider) []string {
	var out []string
	for _, s := range p.Steps {
		if s.Action != engine.Keep && !prov.Supports(s.Stack) {
			out = append(out, s.Stack)
		}
	}
	return out
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
