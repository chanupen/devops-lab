package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/engine"
	labaws "github.com/chanupen/devops-lab/internal/provider/aws"
	"github.com/chanupen/devops-lab/internal/reaper/sweep"
	"github.com/chanupen/devops-lab/internal/state"
)

// cloudRunner is the cloud lab's provider: the stack router plus what only
// a cloud lab has (account, expiry, leftovers).
type cloudRunner struct {
	labRunner
	*labaws.Provider
}

// Supports, Apply and Destroy come from the router, which also holds the
// GitOps stacks; the embedded Provider only knows registry and cluster.
func (c cloudRunner) Supports(stack string) bool { return c.labRunner.Supports(stack) }

// Apply implements engine.Runner.
func (c cloudRunner) Apply(ctx context.Context, stack string, log io.Writer) error {
	return c.labRunner.Apply(ctx, stack, log)
}

// Destroy implements engine.Runner.
func (c cloudRunner) Destroy(ctx context.Context, stack string, log io.Writer) error {
	return c.labRunner.Destroy(ctx, stack, log)
}

// Release implements engine.Releaser. It is spelled out because the
// embedded Provider could otherwise make the promoted method ambiguous,
// and a lost Release leaves secrets behind after lab down (session 5).
func (c cloudRunner) Release(ctx context.Context, stack string, log io.Writer) error {
	return c.labRunner.Release(ctx, stack, log)
}

var (
	_ engine.Releaser = cloudRunner{}
	_ engine.Releaser = cloudSampleDBRunner{}
)

// cloudLab is what lab up, lab down and lab extend need beyond a provider.
type cloudLab interface {
	Account() string
	PreflightCheck(ctx context.Context, w io.Writer) error
	Extend(ctx context.Context, d time.Duration, log io.Writer) (time.Time, []string, error)
	Leftovers(ctx context.Context) (labaws.LeftoverReport, error)
	Sweep(ctx context.Context, log io.Writer) (sweep.Result, error)
	Finish() error
}

// newCloudProvider resolves the lab profile's session (the AWS CLI's cached
// one when valid, else one MFA code) and builds the cloud provider.
func newCloudProvider(cfg config.Config, home string, opts runOptions) (*labaws.Provider, error) {
	if opts.RepoRoot == "" {
		return nil, errors.New("cloud mode needs the repo: run lab from the repo or keep ~/.lab/lab.yaml a symlink into it")
	}
	tofu, err := exec.LookPath("tofu")
	if err != nil {
		return nil, errors.New("tofu not found: install OpenTofu ≥ 1.10 (lab doctor --mode cloud checks it)")
	}
	cache, err := labaws.DefaultCacheDir()
	if err != nil {
		return nil, err
	}
	s := labaws.Session{Profile: labaws.DefaultProfile, CacheDir: cache, Prompt: opts.MFAPrompt}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return labaws.New(ctx, s, labaws.Options{
		RepoRoot:   opts.RepoRoot,
		Home:       home,
		TofuHome:   filepath.Join(filepath.Dir(home), "tofu"),
		Tofu:       tofu,
		Kubeconfig: filepath.Join(home, "kubeconfig"),
		ChartCache: chartCache("cloud", home),
		Profile:    labaws.DefaultProfile,
		Owner:      cfg.Cloud.Owner,
		TTL:        time.Duration(cfg.Cloud.TTL),
		K8sVersion: cfg.Cloud.K8sVersion,
		Timeout:    opts.Timeout,
		Stacks:     activeStacks(cfg, home, opts.Stacks),
	})
}

// cloudRunOptions fills the run options cloud mode needs: the repo and,
// on a terminal, the MFA prompt.
func (f *labFlags) cloudRunOptions(cmd *cobra.Command, o runOptions) runOptions {
	if p, err := f.configPath(); err == nil && p != "" {
		if repo, err := repoRoot(p); err == nil {
			o.RepoRoot = repo
		}
	}
	if in, ok := cmd.InOrStdin().(*os.File); ok && isatty.IsTerminal(in.Fd()) {
		o.MFAPrompt = terminalMFA(in, cmd.ErrOrStderr())
	}
	return o
}

// confirmCloudApply checks that the dead man's switch is armed (the reaper
// deletes for real and ran cleanly in the last 30 minutes) and the
// Kubernetes version is in standard support and fits the pinned
// components (labaws.ChooseVersion), then asks before
// lab up creates or changes AWS resources.
func confirmCloudApply(cmd *cobra.Command, prov provider, p engine.Plan, ttl time.Duration, yes bool) error {
	c, ok := prov.(cloudLab)
	if !ok || p.Count(engine.Apply) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), time.Minute)
	defer cancel()
	if err := c.PreflightCheck(ctx, cmd.ErrOrStderr()); err != nil {
		return err
	}
	return confirm(cmd, yes, fmt.Sprintf("Apply %d stacks to AWS account %s in %s? They bill until lab down or expires-at (TTL %s).",
		p.Count(engine.Apply), c.Account(), labaws.Region, ttl))
}

// reportCloudDown runs after a cloud lab down: when no stack is left it
// sweeps what is left of this environment (Sweep), clears the expiry,
// then lists every lab=true resource that still exists
// outside the baseline, because anything there may still bill.
func reportCloudDown(cmd *cobra.Command, prov provider, store *state.Store) error {
	c, ok := prov.(cloudLab)
	if !ok {
		return nil
	}
	st, err := store.Load()
	if err != nil {
		return err
	}
	if len(st.Stacks) > 0 {
		return nil
	}
	// The sweep needs this environment's expires-at, which Finish clears.
	sctx, scancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
	swept, sweepErr := c.Sweep(sctx, cmd.ErrOrStderr())
	scancel()
	if sweepErr == nil {
		if err := writeSweep(cmd.OutOrStdout(), swept); err != nil {
			return err
		}
		if len(swept.Errors) > 0 {
			sweepErr = fmt.Errorf("the sweep could not delete or check %d resources (above)", len(swept.Errors))
		}
	} else {
		sweepErr = fmt.Errorf("sweep after destroy: %w", sweepErr)
	}
	if err := c.Finish(); err != nil {
		return errors.Join(sweepErr, err)
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), time.Minute)
	defer cancel()
	left, err := c.Leftovers(ctx)
	if err != nil {
		return errors.Join(sweepErr, fmt.Errorf("list resources still tagged lab=true: %w", err))
	}
	return errors.Join(sweepErr, writeLeftovers(cmd.OutOrStdout(), left))
}

// writeSweep prints what the final sweep deleted, what was still in use
// and what it could not delete or check.
func writeSweep(w io.Writer, r sweep.Result) error {
	var b strings.Builder
	if len(r.Deleted)+len(r.Waiting)+len(r.Errors) == 0 {
		b.WriteString("\nsweep after destroy: nothing of this lab left (network interfaces, EBS volumes, secrets)\n")
	}
	if len(r.Deleted) > 0 {
		fmt.Fprintf(&b, "\nsweep after destroy deleted (%d): tofu destroy and the stacks' release left these:\n", len(r.Deleted))
		for _, t := range r.Deleted {
			fmt.Fprintf(&b, "  %s\n", labaws.ShortARN(t.ARN))
		}
	}
	if len(r.Waiting) > 0 {
		fmt.Fprintf(&b, "\nsweep after destroy: still in use, not deleted (%d):\n", len(r.Waiting))
		for _, s := range r.Waiting {
			fmt.Fprintf(&b, "  %s\n", s)
		}
	}
	if len(r.Errors) > 0 {
		fmt.Fprintf(&b, "\nsweep after destroy: failed (%d):\n", len(r.Errors))
		for _, s := range r.Errors {
			fmt.Fprintf(&b, "  %s\n", s)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeLeftovers prints what is really left, then the stale tagging-index
// entries it ignored, then lookups that failed.
func writeLeftovers(w io.Writer, rep labaws.LeftoverReport) error {
	var b strings.Builder
	if len(rep.Left) == 0 {
		b.WriteString("\nnothing tagged lab=true is left outside the baseline\n")
	} else {
		fmt.Fprintf(&b, "\nstill tagged lab=true outside the baseline (%d); these may still bill:\n", len(rep.Left))
		for _, t := range rep.Left {
			fmt.Fprintf(&b, "  %s  expires-at=%s\n", labaws.ShortARN(t.ARN), orDash(t.Tags[labaws.ExpiresTag]))
		}
	}
	if len(rep.Stale) > 0 {
		fmt.Fprintf(&b, "\nstale index entries (ignored) (%d): the tagging API still lists these, but they are terminated or not found:\n", len(rep.Stale))
		for _, t := range rep.Stale {
			fmt.Fprintf(&b, "  %s\n", labaws.ShortARN(t.ARN))
		}
	}
	if len(rep.Errors) > 0 {
		b.WriteString("\ncould not check (listed above as still there):\n")
		for _, e := range rep.Errors {
			fmt.Fprintf(&b, "  %s\n", e)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func newExtendCmd(newProvider providerFactory) *cobra.Command {
	var flags labFlags
	cmd := &cobra.Command{
		Use:   "extend [duration]",
		Short: "Push the cloud lab's expires-at forward (default 1h)",
		Long: "Moves expires-at forward from the later of now and the current expiry, on every AWS\n" +
			"resource the cloud lab created (through the tagging API), on Karpenter's EC2NodeClass\n" +
			"and in the Load Balancer Controller's defaultTags. The TTL reaper reads that tag.\n" +
			"expires-at never goes more than 8h ahead of now.",
		Example: "  lab extend\n  lab extend 2h",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d := time.Hour
			if len(args) == 1 {
				var err error
				if d, err = time.ParseDuration(args[0]); err != nil || d <= 0 {
					return fmt.Errorf("duration %q: want a positive Go duration such as 1h or 90m", args[0])
				}
			}
			flags.mode = "cloud"
			cfg, store, _, err := flags.load()
			if err != nil {
				return err
			}
			st, err := store.Load()
			if err != nil {
				return err
			}
			if len(st.Stacks) == 0 {
				return errors.New("no cloud lab is deployed: lab up --mode cloud starts one")
			}
			prov, err := newProvider(cfg, homeOf(store), flags.cloudRunOptions(cmd, runOptions{}))
			if err != nil {
				return err
			}
			c, ok := prov.(cloudLab)
			if !ok {
				return errors.New("the cloud provider cannot extend")
			}
			out := cmd.OutOrStdout()
			exp, failed, err := c.Extend(cmd.Context(), d, out)
			for _, f := range failed {
				_, _ = fmt.Fprintf(out, "warning: not retagged: %s\n", f)
			}
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "expires-at %s (in %s)\n", exp.Local().Format("2006-01-02 15:04 MST"),
				time.Until(exp).Round(time.Minute))
			return err
		},
	}
	flags.register(cmd)
	return cmd
}

func newCloudPlanCmd(newProvider providerFactory) *cobra.Command {
	var (
		flags   labFlags
		verbose bool
	)
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show the OpenTofu plans for the cloud registry and cluster stacks; changes nothing",
		Long: "Runs tofu plan for infra/terraform/aws/cluster/registry (ECR) and infra/terraform/aws/cluster\n" +
			"(VPC, EKS, Karpenter IAM, Pod Identity) against the state bucket and prints both plans.\n" +
			"Nothing is applied: lab up --mode cloud does that, after asking.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.mode = "cloud"
			cfg, store, _, err := flags.load()
			if err != nil {
				return err
			}
			prov, err := newProvider(cfg, homeOf(store), flags.cloudRunOptions(cmd, runOptions{}))
			if err != nil {
				return err
			}
			c, ok := prov.(interface {
				Plan(context.Context, io.Writer) ([]labaws.ModulePlan, error)
			})
			if !ok {
				return errors.New("the cloud provider cannot plan")
			}
			log := io.Discard
			if verbose {
				log = cmd.ErrOrStderr()
			}
			plans, err := c.Plan(cmd.Context(), log)
			out := cmd.OutOrStdout()
			for _, p := range plans {
				_, _ = fmt.Fprintf(out, "## %s (%s)\n\n%s\n", p.Stack, map[bool]string{true: "changes", false: "no changes"}[p.Changes], p.Text)
			}
			return err
		},
	}
	flags.register(cmd)
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "also print tofu init and plan progress on stderr")
	return cmd
}
