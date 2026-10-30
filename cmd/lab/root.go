package main

import "github.com/spf13/cobra"

func newRootCmd() *cobra.Command { return newRootCmdWith(defaultProvider) }

// newRootCmdWith lets tests swap the provider for a fake.
func newRootCmdWith(newProvider providerFactory) *cobra.Command {
	root := &cobra.Command{
		Use:           "lab",
		Short:         "DevOps/SRE home lab control center",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newDoctorCmd(), newPlanCmd(), newUpCmd(newProvider), newDownCmd(newProvider), newStatusCmd(newProvider))
	return root
}
