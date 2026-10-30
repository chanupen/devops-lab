package main

import (
	"bufio"
	"context"
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
	hosts func(context.Context) ([]string, error) // nil without a GitOps runner
	url   func(host string) string
	hint  string
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
}

// providerFactory builds the runner for cfg.Mode. home is the directory
// holding the state file; generated files (kubeconfig, configs) go under it.
type providerFactory func(cfg config.Config, home string, opts runOptions) (provider, error)

// defaultProvider routes infrastructure stacks to the mode's provider and
// in-cluster stacks to the GitOps runner, which is the same in every mode.
func defaultProvider(cfg config.Config, home string, opts runOptions) (provider, error) {
	kubeconfig := filepath.Join(home, "kubeconfig")
	l := labRunner{Router: engine.Router{}}
	switch cfg.Mode {
	case "local":
		p, err := local.New(local.Options{Home: home, Kubeconfig: kubeconfig, K8sVersion: cfg.K8sVersion, Purge: opts.Purge})
		if err != nil {
			return nil, err
		}
		l.Router["registry"], l.Router["cluster"] = p, p
		l.url, l.hint = p.AppURL, p.AccessHint()
	default:
		return nil, fmt.Errorf("%s mode has no provider yet", cfg.Mode)
	}
	if cfg.GitOps == "argocd" { // Flux is the second option and not built yet
		g := gitops.New(gitops.Options{Mode: cfg.Mode, Repo: cfg.Repo, Revision: opts.Revision, Timeout: opts.Timeout}, kubeconfig, filepath.Join(home, "helm"))
		for _, s := range []string{"gitops", "core", "obs", "sample-app"} {
			l.Router[s] = g
		}
		l.hosts = g.AppHosts
	}
	return l, nil
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
