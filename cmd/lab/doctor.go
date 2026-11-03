package main

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/doctor"
	"github.com/chanupen/devops-lab/internal/provider/local"
)

func newDoctorCmd() *cobra.Command {
	var (
		asJSON bool
		mode   string
		opts   doctor.Options
	)
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that the host is ready to run the lab",
		Long: "Checks Podman, cgroup delegation, kernel limits and modules, SELinux, the GPU stack,\n" +
			"Ollama, tool versions and free memory. With --mode cloud it checks OpenTofu, that the\n" +
			"lab AWS profile assumes lab-admin, and that the state bucket and budget exist. Exits\n" +
			"non-zero if any check fails.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := doctor.Checks()
			switch mode {
			case "local":
				if err := requireLinux(runtime.GOOS, cmd.ErrOrStderr()); err != nil {
					return err
				}
			case "cloud":
				checks = doctor.CloudChecks(cloudProbe(cmd.Context(), cmd, !asJSON))
			default:
				return fmt.Errorf("--mode %q: want local or cloud", mode)
			}
			sys := doctor.HostSystem()
			if mode == "local" {
				sys.Units = doctorUnits
				opts.K8sVersion = config.K8sVersion
				if cfg, err := (&labFlags{}).loadConfigOnly(); err == nil {
					opts.K8sVersion = cfg.K8sVersion
				}
			}
			results := doctor.Run(cmd.Context(), sys, opts, checks)
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
	cmd.Flags().StringVar(&mode, "mode", "local", "local: this host; cloud: OpenTofu, the lab profile, the state bucket and the budget")
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

// doctorUnits lists the lab's containers over the Podman API for lab doctor.
func doctorUnits(ctx context.Context) ([]doctor.Unit, error) {
	units, err := local.HostUnits(ctx)
	out := make([]doctor.Unit, 0, len(units))
	for _, u := range units {
		d := doctor.Unit{Kind: u.Kind, Name: u.Name, Nodes: len(u.Containers), Running: u.Running(), Fix: u.Fix()}
		if u.Kind == "cluster" && u.Name == local.ClusterName && d.Running {
			d.Version, d.VersionErr = clusterVersion(ctx)
		}
		out = append(out, d)
	}
	return out, err
}

// clusterVersion reads the main cluster's version from its node, as
// strings for a report: the version, or why it could not be read.
func clusterVersion(ctx context.Context) (version, why string) {
	v, err := local.HostClusterVersion(ctx)
	if err != nil {
		return "", err.Error()
	}
	return v, ""
}
