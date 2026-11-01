package main

import (
	"fmt"
	"strings"
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
		stacks  []string
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Destroy every deployed stack, dependents first",
		Long: "Destroys every stack recorded in ~/.lab/state.json in reverse dependency order. With\n" +
			"--stacks, destroys only those stacks and the deployed stacks that need them. In local\n" +
			"mode the registry's image cache volume is kept unless --purge is given.",
		Example: "  lab down\n  lab down --stacks sample-app",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, store, g, err := flags.load()
			if err != nil {
				return err
			}
			command := downCommand(stacks, purge)
			p, err := engine.New(g, store, nil).PlanDown(cfg, engine.DownOptions{Stacks: stacks})
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
			prov, err := newProvider(cfg, homeOf(store), runOptions{Purge: purge, Timeout: timeout})
			if err != nil {
				return err
			}
			return run(cmd, prov, g, store, p, command)
		},
	}
	flags.register(cmd)
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove the registry's image cache volume")
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	cmd.Flags().StringSliceVar(&stacks, "stacks", nil, "destroy only these stacks and the deployed stacks that need them")
	registerTimeout(cmd, &timeout)
	return cmd
}

// downCommand is the CLI command recorded in the ops log.
func downCommand(stacks []string, purge bool) string {
	c := "lab down"
	if len(stacks) > 0 {
		c += " --stacks " + strings.Join(stacks, ",")
	}
	if purge {
		c += " --purge"
	}
	return c
}
