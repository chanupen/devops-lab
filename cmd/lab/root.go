package main

import "github.com/spf13/cobra"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "lab",
		Short:         "DevOps/SRE home lab control center",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newDoctorCmd(), newPlanCmd())
	return root
}
