package main

import "github.com/spf13/cobra"

func newRootCmd() *cobra.Command { return newRootCmdWith(defaultProvider) }

// newRootCmdWith lets tests swap the provider for a fake.
func newRootCmdWith(newProvider providerFactory) *cobra.Command {
	var flags labFlags
	root := &cobra.Command{
		Use:           "lab",
		Short:         "DevOps/SRE home lab control center",
		Long:          "With no command, lab opens the TUI. Every TUI action is also one of the commands below.",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, _ []string) error { return runTUI(cmd, flags) },
	}
	flags.register(root)
	root.AddCommand(newDoctorCmd(), newPlanCmd(), newUpCmd(newProvider), newDownCmd(newProvider), newStatusCmd(newProvider))
	return root
}
