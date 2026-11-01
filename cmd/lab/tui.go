package main

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
	opts, err := tuiOptions(flags, cmd.Flags().Changed("config"))
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

// tuiOptions loads lab.yaml and the state file. A missing lab.yaml is fine
// when --config was not given (ssh -t fedora lab starts in the home
// directory): the defaults apply and state.json still says what is running.
func tuiOptions(flags labFlags, configSet bool) (tui.Options, error) {
	cfg, err := config.Load(flags.cfgPath)
	switch {
	case errors.Is(err, fs.ErrNotExist) && !configSet:
		cfg = config.Default()
	case err != nil:
		return tui.Options{}, err
	}
	path := flags.statePath
	if path == "" {
		if path, err = state.DefaultPath(); err != nil {
			return tui.Options{}, err
		}
	}
	st, err := state.NewStore(path).Load()
	if err != nil {
		return tui.Options{}, err
	}
	opts := tui.Options{
		Config:     cfg,
		State:      st,
		StatePath:  path,
		Kubeconfig: filepath.Join(filepath.Dir(path), "kubeconfig"),
		Catalog:    engine.Catalog(),
	}
	mode := st.Mode
	if mode == "" {
		mode = cfg.Mode
	}
	if mode == "local" {
		opts.AppURL, opts.AccessHint = local.AppURL, local.AccessHint()
	}
	return opts, nil
}
