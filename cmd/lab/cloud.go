package main

import (
	"bufio"
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

	"github.com/chanupen/devops-lab/internal/compat"
	"github.com/chanupen/devops-lab/internal/doctor"
	labaws "github.com/chanupen/devops-lab/internal/provider/aws"
	"github.com/chanupen/devops-lab/internal/state"
)

// baselineVarFile holds the baseline's private variables (alert_email).
// It lives under ~/.lab, never in the repo.
const baselineVarFile = "aws/baseline.tfvars"

func newCloudCmd() *cobra.Command {
	cloud := &cobra.Command{
		Use:   "cloud",
		Short: "Cloud mode: AWS session and account baseline (lab up --mode cloud runs the lab itself)",
	}
	cloud.AddCommand(newCloudLoginCmd(), newBaselineCmd(), newCloudPlanCmd(defaultProvider), newLeftoversCmd())
	return cloud
}

type cloudFlags struct {
	labFlags
	profile   string
	module    string
	reaperZip string
}

func (f *cloudFlags) register(cmd *cobra.Command) {
	f.labFlags.register(cmd)
	cmd.Flags().StringVar(&f.profile, "profile", labaws.DefaultProfile,
		"AWS CLI profile; use devops for the first apply, before lab-admin exists")
}

// registerBaseline adds the flags that locate the module and the reaper.
func (f *cloudFlags) registerBaseline(cmd *cobra.Command) {
	f.register(cmd)
	cmd.Flags().StringVar(&f.module, "module", "", "baseline module directory (default: infra/terraform/aws/baseline next to lab.yaml)")
	cmd.Flags().StringVar(&f.reaperZip, "reaper-zip", "", "reaper Lambda package (default: bin/reaper.zip next to lab.yaml; make reaper builds it)")
}

// labHome is ~/.lab, or the directory of --state.
func (f *labFlags) labHome() (string, error) {
	if f.statePath != "" {
		return filepath.Dir(f.statePath), nil
	}
	p, err := state.DefaultPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(p), nil
}

// session is the AWS session for the profile, asking for the MFA code on
// the terminal when the profile needs one and no cached session is valid.
func (f *cloudFlags) session(cmd *cobra.Command) (labaws.Session, error) {
	cache, err := labaws.DefaultCacheDir()
	if err != nil {
		return labaws.Session{}, err
	}
	s := labaws.Session{Profile: f.profile, CacheDir: cache}
	if in, ok := cmd.InOrStdin().(*os.File); ok && isatty.IsTerminal(in.Fd()) {
		s.Prompt = terminalMFA(in, cmd.ErrOrStderr())
	}
	return s, nil
}

func terminalMFA(in io.Reader, out io.Writer) func(string) (string, error) {
	r := bufio.NewReader(in)
	return func(serial string) (string, error) {
		_, _ = fmt.Fprintf(out, "MFA code for %s: ", serial)
		code, err := r.ReadString('\n')
		code = strings.TrimSpace(code)
		if code == "" && err != nil {
			return "", err
		}
		return code, nil
	}
}

func newCloudLoginCmd() *cobra.Command {
	var flags cloudFlags
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Assume lab-admin with MFA and cache the session for up to 4 hours (shared with the aws CLI)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := flags.session(cmd)
			if err != nil {
				return err
			}
			// Without a terminal there is no way to ask for the code: say so
			// before Forget drops a session that still works.
			if s.Prompt == nil {
				return errLoginNeedsTerminal
			}
			if err := s.Forget(cmd.Context()); err != nil { // a login always asks for a fresh session
				return err
			}
			cfg, err := s.Config(cmd.Context())
			if err != nil {
				return err
			}
			arn, account, err := labaws.Identity(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "logged in to account %s as %s\n", account, arn)
			return err
		},
	}
	flags.register(cmd)
	return cmd
}

// errLoginNeedsTerminal: lab cloud login reads the MFA code from a
// terminal (an agent's shell or a pipe has none).
var errLoginNeedsTerminal = errors.New("lab cloud login reads the MFA code from a terminal, and stdin is not one; " +
	"run it in a terminal (from the Mac: ssh -t fedora lab cloud login). The cached session was kept")

func newBaselineCmd() *cobra.Command {
	base := &cobra.Command{
		Use:   "baseline",
		Short: "AWS account baseline: state bucket, lab-admin role, budget and TTL reaper",
		Long: "Runs infra/terraform/aws/baseline with OpenTofu. The first apply uses local state (in\n" +
			"~/.lab/tofu/baseline) and then moves it into the state bucket it created. Run the\n" +
			"first apply with --profile devops: the lab profile assumes lab-admin, which the\n" +
			"baseline creates. Budget alerts go to alert_email in ~/.lab/" + baselineVarFile + ".",
	}
	var planFlags, applyFlags cloudFlags
	plan := &cobra.Command{
		Use:   "plan",
		Short: "Show what the baseline would change; changes nothing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := planFlags.baseline(cmd)
			if err != nil {
				return err
			}
			changes, remote, err := b.Plan(cmd.Context())
			if err != nil {
				return err
			}
			return writeBaselineSummary(cmd.OutOrStdout(), changes, remote, false)
		},
	}
	apply := &cobra.Command{
		Use:   "apply",
		Short: "Show the plan, ask, then apply it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := applyFlags.baseline(cmd)
			if err != nil {
				return err
			}
			changes, remote, err := b.Plan(cmd.Context())
			if err != nil {
				return err
			}
			if !changes {
				return writeBaselineSummary(cmd.OutOrStdout(), false, remote, true)
			}
			if err := confirm(cmd, false, fmt.Sprintf("Apply this plan to AWS account %s in %s?", b.Account, labaws.Region)); err != nil {
				return err
			}
			migrated, err := b.Apply(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if migrated {
				_, _ = fmt.Fprintf(out, "\nstate moved into s3://%s/%s; local state is no longer used\n", labaws.StateBucket(b.Account), labaws.StateKey)
			}
			outs, err := b.Outputs(cmd.Context())
			if err != nil {
				return err
			}
			for _, k := range []string{"state_bucket", "lab_admin_role_arn", "budget_name", "reaper_function", "reaper_dry_run"} {
				_, _ = fmt.Fprintf(out, "  %-18s %s\n", k, outs[k])
			}
			return nil
		},
	}
	planFlags.registerBaseline(plan)
	applyFlags.registerBaseline(apply)
	withLabLock(apply) // plan only reads
	base.AddCommand(plan, apply)
	return base
}

func writeBaselineSummary(out io.Writer, changes, remote, applying bool) error {
	where := "local (moves into the state bucket after the first apply)"
	if remote {
		where = "in the state bucket"
	}
	msg := fmt.Sprintf("\nstate: %s\n", where)
	switch {
	case !changes:
		msg += "no changes: the baseline matches the code\n"
	case !applying:
		msg += "to apply it: lab cloud baseline apply\n"
	}
	_, err := io.WriteString(out, msg)
	return err
}

// baseline builds the runner: config, repo paths, session, account.
func (f *cloudFlags) baseline(cmd *cobra.Command) (*labaws.Baseline, error) {
	ctx := cmd.Context()
	cfgPath, err := f.configPath()
	if err != nil {
		return nil, err
	}
	if cfgPath == "" {
		return nil, errors.New("no lab.yaml in this directory or ~/.lab; pass --config")
	}
	cfg, _, _, err := f.load()
	if err != nil {
		return nil, err
	}
	if cfg.Cloud.Owner == "" {
		return nil, errors.New("lab.yaml needs cloud.owner (it becomes the owner tag)")
	}
	repo, err := repoRoot(cfgPath)
	if err != nil {
		return nil, err
	}
	module := orDefault(f.module, filepath.Join(repo, "infra", "terraform", "aws", "baseline"))
	zip := orDefault(f.reaperZip, filepath.Join(repo, "bin", "reaper.zip"))
	if _, err := os.Stat(zip); err != nil {
		return nil, fmt.Errorf("reaper package %s is missing: run make reaper in %s", zip, repo)
	}
	if zip, err = filepath.Abs(zip); err != nil {
		return nil, err
	}
	tofu, err := exec.LookPath("tofu")
	if err != nil {
		return nil, errors.New("tofu not found: install OpenTofu ≥ 1.10 (lab doctor --mode cloud checks it)")
	}
	home, err := f.labHome()
	if err != nil {
		return nil, err
	}
	varFile := filepath.Join(home, baselineVarFile)
	if _, err := os.Stat(varFile); err != nil {
		return nil, fmt.Errorf("%s is missing: create it with alert_email = \"you@example.com\" (budget alerts go there)", varFile)
	}
	s, err := f.session(cmd)
	if err != nil {
		return nil, err
	}
	awsCfg, err := s.Config(ctx)
	if err != nil {
		return nil, err
	}
	arn, account, err := labaws.Identity(ctx, awsCfg)
	if err != nil {
		return nil, err
	}
	env, err := s.Env(ctx, awsCfg)
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "AWS account %s as %s, region %s\n", account, arn, labaws.Region)
	return &labaws.Baseline{
		ModuleDir: module,
		WorkDir:   filepath.Join(home, "tofu", "baseline"),
		Tofu:      tofu,
		VarFile:   varFile,
		Vars:      map[string]any{"owner": cfg.Cloud.Owner, "reaper_zip": zip},
		Env:       env,
		Account:   account,
		Config:    awsCfg,
		Out:       cmd.OutOrStdout(),
	}, nil
}

// repoRoot is the directory of lab.yaml after symlinks: ~/.lab/lab.yaml
// points into the repo.
func repoRoot(cfgPath string) (string, error) {
	resolved, err := filepath.EvalSymlinks(cfgPath)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	return filepath.Dir(abs), nil
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// cloudProbe resolves the lab profile's session once, before doctor's
// checks run, asking for the MFA code only when interactive.
func cloudProbe(ctx context.Context, cmd *cobra.Command, interactive bool) doctor.CloudProbe {
	p := doctor.CloudProbe{Profile: labaws.DefaultProfile, BucketName: labaws.StateBucket, BudgetName: labaws.BudgetName}
	pin := ""
	var active []string
	if cfg, err := (&labFlags{}).loadConfigOnly(); err == nil {
		pin = cfg.Cloud.K8sVersion
		if path, err := state.PathFor("cloud"); err == nil {
			active = activeStacks(cfg, filepath.Dir(path), nil)
		}
	}
	cache, err := labaws.DefaultCacheDir()
	if err != nil {
		p.SessionErr = err
		return p
	}
	s := labaws.Session{Profile: labaws.DefaultProfile, CacheDir: cache}
	if in, ok := cmd.InOrStdin().(*os.File); interactive && ok && isatty.IsTerminal(in.Fd()) {
		s.Prompt = terminalMFA(in, cmd.ErrOrStderr())
	}
	cfg, err := s.Config(ctx)
	if err != nil {
		p.SessionErr = err
		return p
	}
	p.Identity = func(ctx context.Context) (string, string, error) { return labaws.Identity(ctx, cfg) }
	p.BucketExists = func(ctx context.Context, account string) (bool, error) { return labaws.BucketExists(ctx, cfg, account) }
	p.BudgetExists = func(ctx context.Context, account string) (bool, error) { return labaws.BudgetExists(ctx, cfg, account) }
	p.EKSVersion = func(ctx context.Context) (string, []string, error) {
		standard, err := labaws.StandardVersions(ctx, cfg)
		if err != nil {
			return "", nil, err
		}
		current, err := labaws.ClusterVersion(ctx, cfg, labaws.ClusterName)
		if err != nil {
			return "", nil, err
		}
		c, err := labaws.ChooseVersion(pin, current, standard, compat.ForStacks("cloud", active))
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Kubernetes %s: %s", c.Version, c.Reason), c.Warnings, nil
	}
	p.TeardownBuild = func(ctx context.Context) (string, error) { return labaws.RunningTeardown(ctx, cfg) }
	p.ReaperReady = func(ctx context.Context, account string) error {
		st, err := labaws.GetReaperStatus(ctx, cfg, account)
		if err != nil {
			return err
		}
		return st.Ready(time.Now())
	}
	return p
}

// newLeftoversCmd lists what a cloud lab left behind, for example to check
// after the teardown build ran that nothing tagged lab=true is left.
func newLeftoversCmd() *cobra.Command {
	var flags cloudFlags
	cmd := &cobra.Command{
		Use:   "leftovers",
		Short: "List everything still tagged lab=true outside the baseline, stale tagging-index entries apart; changes nothing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := flags.session(cmd)
			if err != nil {
				return err
			}
			cfg, err := s.Config(cmd.Context())
			if err != nil {
				return err
			}
			left, err := labaws.Leftovers(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			if err := writeLeftovers(cmd.OutOrStdout(), left); err != nil {
				return err
			}
			if len(left.Left) > 0 {
				return exitError{code: 1}
			}
			return nil
		},
	}
	flags.register(cmd)
	return cmd
}
