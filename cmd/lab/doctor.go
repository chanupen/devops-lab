package main

import (
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/doctor"
)

func newDoctorCmd() *cobra.Command {
	var (
		asJSON bool
		opts   doctor.Options
	)
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that the host is ready to run the lab",
		Long: "Checks Podman, cgroup delegation, kernel limits and modules, SELinux, the GPU stack,\n" +
			"Ollama, tool versions and free memory. Exits non-zero if any check fails.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireLinux(runtime.GOOS, cmd.ErrOrStderr()); err != nil {
				return err
			}
			results := doctor.Run(cmd.Context(), doctor.HostSystem(), opts, doctor.Checks())
			out := cmd.OutOrStdout()
			var err error
			if asJSON {
				err = doctor.WriteJSON(out, opts.Profile, results)
			} else {
				err = doctor.WriteTable(out, lipgloss.NewRenderer(out), results)
			}
			if err != nil {
				return err
			}
			if doctor.Failed(results) {
				return exitError{code: 1}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print results as JSON")
	cmd.Flags().StringVar(&opts.Profile, "profile", config.Default().Profile, "memory profile to check against: minimal, standard, full")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", 10*time.Second, "timeout per check")
	return cmd
}

// requireLinux stops doctor on the client machine: every check reads Linux host state.
func requireLinux(goos string, stderr io.Writer) error {
	if goos == "linux" {
		return nil
	}
	_, _ = fmt.Fprintln(stderr, "lab doctor checks the Linux host. Run it there: ssh -t fedora lab doctor")
	return exitError{code: 2}
}
