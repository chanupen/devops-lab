package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/doctor"
	"github.com/chanupen/devops-lab/internal/kube"
	"github.com/chanupen/devops-lab/internal/provider/local"
	"github.com/chanupen/devops-lab/internal/state"
)

func newDoctorCmd(deps guidedDeps) *cobra.Command {
	var (
		asJSON   bool
		readOnly bool
		fix      bool
		mode     string
		opts     doctor.Options
		flags    labFlags
	)
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that the host is ready to run the lab",
		Long: "Checks Podman, cgroup delegation, kernel limits and modules, SELinux, the GPU stack,\n" +
			"Ollama, tool versions and free memory. With --mode cloud it checks OpenTofu, that the\n" +
			"lab AWS profile assumes lab-admin, and that the state bucket and budget exist. Exits\n" +
			"non-zero if any check fails.\n" +
			"With --fix, print exact remediation commands and approval/sudo labels only; never\n" +
			"execute them. Registry upload permission is untested. Supports --json.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := deps.checks()
			switch mode {
			case "local":
				if err := requireLinux(deps.goos, cmd.ErrOrStderr()); err != nil {
					return err
				}
			case "cloud":
				if readOnly || flags.cfgPath != "" || flags.statePath != "" {
					return fmt.Errorf("--read-only, --config and --state apply to local doctor reports only")
				}
				checks = doctor.CloudChecks(cloudProbe(cmd.Context(), cmd, !asJSON && !fix, fix))
			default:
				return fmt.Errorf("--mode %q: want local or cloud", mode)
			}
			sys := doctor.HostSystem()
			if mode == "local" {
				cfg, err := flags.loadConfigOnly()
				if err != nil {
					if flags.cfgPath != "" {
						return err
					}
					cfg = config.Default()
				}
				path := flags.statePath
				if path == "" {
					path, err = state.DefaultPath()
					if err != nil {
						return err
					}
				}
				st, err := state.NewStore(path).Load()
				if err != nil && flags.statePath != "" {
					return err
				}
				sys = deps.system(filepath.Dir(path), readOnly || fix)
				opts = localDoctorOptions(cfg, st, drillState(path, state.E2EPath(path)), opts)
				if fix {
					if err != nil {
						return err
					}
					e2e, err := state.NewStore(state.E2EPath(path)).Load()
					if err != nil {
						return err
					}
					selected := guidedInput{cfg: cfg, st: st, e2e: e2e, store: state.NewStore(path)}
					opts.Drill = selected.drillReport(deps.now())
					opts.StatePath, err = filepath.Abs(path)
					if err != nil {
						return err
					}
					cfgPath, err := flags.configPath()
					if err != nil {
						return err
					}
					if cfgPath != "" {
						opts.ConfigPath, err = filepath.Abs(cfgPath)
						if err != nil {
							return err
						}
					}
					opts.Kubeconfig = filepath.Join(filepath.Dir(opts.StatePath), "kubeconfig")
				}
			}
			var results []doctor.Result
			if fix && mode == "local" {
				results = doctor.RunObserved(cmd.Context(), sys, opts, checks)
			} else {
				results = doctor.Run(cmd.Context(), sys, opts, checks)
			}
			out := cmd.OutOrStdout()
			var err error
			switch {
			case fix:
				err = doctor.WriteFixes(out, opts, results, asJSON)
			case asJSON:
				err = doctor.WriteJSON(out, opts.Profile, results)
			default:
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
	cmd.Flags().BoolVar(&fix, "fix", false, "print fix commands only; never execute them (registry write permission untested)")
	cmd.Flags().BoolVar(&readOnly, "read-only", false, "observe the local registry without testing upload permission")
	cmd.Flags().StringVar(&mode, "mode", "local", "local: this host; cloud: OpenTofu, the lab profile, the state bucket and the budget")
	cmd.Flags().StringVar(&opts.Profile, "profile", config.Default().Profile, "memory profile to check against: minimal, standard, full")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", 10*time.Second, "timeout per check")
	flags.register(cmd)
	return cmd
}

func doctorGitOpsAt(ctx context.Context, home string) (doctor.GitOpsState, error) {
	cfg, err := kube.RESTConfig(filepath.Join(home, "kubeconfig"))
	if err != nil {
		return doctor.GitOpsState{}, err
	}
	clients, err := kube.NewClients(kube.WarningsTo(cfg, io.Discard))
	if err != nil {
		return doctor.GitOpsState{}, err
	}
	app, err := clients.Dynamic.Resource(kube.AppGVR).Namespace("argocd").Get(ctx, "lab-bootstrap", metav1.GetOptions{})
	if err != nil {
		return doctor.GitOpsState{}, err
	}
	revision, _, _ := unstructured.NestedString(app.Object, "spec", "source", "targetRevision")
	return doctor.GitOpsState{Revision: revision, Problems: kube.AppFrom(app).Problems}, nil
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

// doctorAI reads the ai stack for lab doctor: the host's Ollama, the
// EndpointSlice behind ollama.ai.svc, Ollama and Langfuse through the
// cluster, and the stack's Argo CD apps.
func doctorAIAt(ctx context.Context, home string) (doctor.AIState, error) {
	st, err := local.HostAIStatus(ctx, filepath.Join(home, "kubeconfig"))
	if err != nil {
		return doctor.AIState{}, err
	}
	return doctor.AIState{Address: st.Address, AddressErr: st.AddressErr, Slice: st.Slice, SliceErr: st.SliceErr,
		Version: st.Version, Models: st.Models, InClusterErr: st.InClusterErr, LangfuseVersion: st.LangfuseVersion,
		LangfuseErr: st.LangfuseErr, Apps: st.Apps, AppsNotReady: st.AppsNotReady}, nil
}

func localDoctorOptions(cfg config.Config, st state.State, drill *doctor.DrillState, opts doctor.Options) doctor.Options {
	opts.K8sVersion, opts.ZotHosts, opts.Drill = cfg.K8sVersion, cfg.Zot.AddHosts, drill
	opts.Stacks = nil
	for name := range st.Stacks {
		if st.IsApplied(name) {
			opts.Stacks = append(opts.Stacks, name)
		}
	}
	slices.Sort(opts.Stacks)
	return opts
}

func localDoctorSystem(home string, readOnly bool) doctor.System {
	sys := doctor.HostSystem()
	var unitsOnce, aiOnce sync.Once
	var units []doctor.Unit
	var unitsErr error
	sys.Units = func(ctx context.Context) ([]doctor.Unit, error) {
		unitsOnce.Do(func() { units, unitsErr = doctorUnits(ctx) })
		return units, unitsErr
	}
	sys.LabRegistry = func(ctx context.Context) doctor.RegistryState {
		probe := local.HostCheckLabRegistry
		if readOnly {
			probe = local.HostObserveLabRegistry
		}
		err := probe(ctx, home)
		r := doctor.RegistryState{WriteUntested: readOnly}
		switch {
		case errors.Is(err, local.ErrRegistryStopped):
			r.Stopped = true
		case errors.Is(err, local.ErrNoRegistry):
			r.Missing = true
		case errors.Is(err, local.ErrNoPullFile):
			r.Pending = err.Error()
		default:
			r.Err = err
		}
		return r
	}
	sys.MotoSecrets = local.NewMotoSecrets(local.MotoEndpointHost).Count
	var ai doctor.AIState
	var aiErr error
	sys.AI = func(ctx context.Context) (doctor.AIState, error) {
		aiOnce.Do(func() { ai, aiErr = doctorAIAt(ctx, home) })
		return ai, aiErr
	}
	sys.GitOps = func(ctx context.Context) (doctor.GitOpsState, error) { return doctorGitOpsAt(ctx, home) }
	repo, digest := local.ZotImageBlob()
	sys.GHCR = doctor.HostGHCRProbe{Repo: repo, Digest: digest}
	return sys
}

func drillState(path, e2e string) *doctor.DrillState {
	if st, err := state.NewStore(path).Load(); err == nil && st.ActiveDrill != nil {
		r := st.ActiveDrill
		label := "drill " + r.Drill
		if r.Random {
			label = "a random drill"
		}
		return &doctor.DrillState{Label: label, Phase: r.Phase, Deadline: r.Deadline, Error: r.Error, Now: time.Now()}
	}
	if st, err := state.NewStore(e2e).Load(); err == nil && st.ActiveDrill != nil {
		r := st.ActiveDrill
		return &doctor.DrillState{Label: "e2e test drill " + r.Drill, Phase: r.Phase, Deadline: r.Deadline, Error: r.Error, Now: time.Now(),
			Fix: "if the e2e test is not running, lab up (or lab drills abort --state " + e2e + ") puts back what the drill changed"}
	}
	return nil
}
