package main

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/engine"
)

func newDownCmd(newProvider providerFactory) *cobra.Command {
	var (
		flags   labFlags
		purge   bool
		yes     bool
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Destroy every deployed stack, dependents first",
		Long: "Destroys every stack recorded in ~/.lab/state.json in reverse dependency order. In local\n" +
			"mode the registry's image cache volume is kept unless --purge is given.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, store, g, err := flags.load()
			if err != nil {
				return err
			}
			p, err := engine.New(g, store, nil).PlanDown(cfg)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if err := writePlan(out, lipgloss.NewRenderer(out), p); err != nil {
				return err
			}
			if len(p.Steps) == 0 {
				return nil
			}
			q := fmt.Sprintf("Destroy %d stacks?", len(p.Steps))
			if purge {
				q = fmt.Sprintf("Destroy %d stacks and purge the image cache?", len(p.Steps))
			}
			if err := confirm(cmd, yes, q); err != nil {
				return err
			}
			command := "lab down"
			if purge {
				command += " --purge"
			}
			return run(cmd, newProvider, cfg, homeOf(store), g, store, p, runOptions{Purge: purge, Timeout: timeout}, command)
		},
	}
	flags.register(cmd)
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove the registry's image cache volume")
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	registerTimeout(cmd, &timeout)
	return cmd
}
