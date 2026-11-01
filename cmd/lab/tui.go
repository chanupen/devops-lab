package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"k8s.io/klog/v2"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/provider/local"
	"github.com/chanupen/devops-lab/internal/state"
	"github.com/chanupen/devops-lab/internal/tui"
)

// runTUI opens the TUI, or prints help when lab is not on a terminal.
func runTUI(cmd *cobra.Command, flags labFlags) error {
	if !isatty.IsTerminal(os.Stdin.Fd()) || !isatty.IsTerminal(os.Stdout.Fd()) {
		return cmd.Help()
	}
	opts, err := tuiOptions(flags, defaultProvider)
	if err != nil {
		return err
	}
	// client-go logs reflector errors through klog to stderr, which would
	// draw over the screen. The TUI shows the cluster as off instead.
	klog.SetOutput(io.Discard)
	klog.LogToStderr(false)
	if trace != nil {
		opts.FirstView = func() { trace.view = time.Now() }
		trace.run = time.Now()
		defer trace.report(os.Stderr)
	}
	return tui.Run(cmd.Context(), opts)
}

// tuiOptions loads lab.yaml and the state file. Without --config it looks
// in ./lab.yaml, then ~/.lab/lab.yaml; if neither exists the defaults apply
// and state.json still says what is running.
func tuiOptions(flags labFlags, newProvider providerFactory) (tui.Options, error) {
	cfgPath, err := flags.configPath()
	if err != nil {
		return tui.Options{}, err
	}
	cfg := config.Default()
	if cfgPath != "" {
		if cfg, err = config.Load(cfgPath); err != nil {
			return tui.Options{}, err
		}
	}
	path := flags.statePath
	if path == "" {
		if path, err = state.DefaultPath(); err != nil {
			return tui.Options{}, err
		}
	}
	store := state.NewStore(path)
	st, err := store.Load()
	if err != nil {
		return tui.Options{}, err
	}
	opts := tui.Options{
		Config:       cfg,
		ConfigPath:   cfgPath,
		State:        st,
		StatePath:    path,
		Kubeconfig:   filepath.Join(filepath.Dir(path), "kubeconfig"),
		Catalog:      engine.Catalog(),
		Actions:      tuiActions{cfg: cfg, cfgPath: cfgPath, store: store, newProvider: newProvider, timeout: 30 * time.Minute},
		BranchExists: branchExists(cfg.Repo),
	}
	mode := st.Mode
	if mode == "" {
		mode = cfg.Mode
	}
	if mode == "local" {
		opts.AppURL, opts.AccessHint = local.AppURL, local.TunnelHint()
	}
	return opts, nil
}

// terminalName is this process's terminal, e.g. /dev/pts/3, so a second lab
// can say where the first one is. "" when it cannot tell.
func terminalName() string {
	for _, p := range []string{"/proc/self/fd/0", "/dev/fd/0"} {
		if t, err := os.Readlink(p); err == nil && (strings.HasPrefix(t, "/dev/pts/") || strings.HasPrefix(t, "/dev/tty")) {
			return t
		}
	}
	if t := os.Getenv("SSH_TTY"); t != "" {
		return t
	}
	return ""
}
