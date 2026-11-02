package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/chanupen/devops-lab/internal/reaper"
)

// ReaperFunction is the baseline's TTL reaper Lambda.
const ReaperFunction = "lab-ttl-reaper"

// ReaperMaxAge is how recent the reaper's last clean run must be before a
// cloud lab may start: two missed 15-minute runs.
const ReaperMaxAge = 30 * time.Minute

// ReaperStatus is what lab needs to know before trusting the dead man's
// switch.
type ReaperStatus struct {
	DryRun    bool              // the function's DRY_RUN setting
	Heartbeat *reaper.Heartbeat // last scheduled run, clean or not; nil when there is none
}

// Ready is nil when the reaper deletes for real and its latest run, within
// ReaperMaxAge of now, was clean and passed its self-test.
func (s ReaperStatus) Ready(now time.Time) error {
	switch {
	case s.DryRun:
		return errors.New("the TTL reaper is in dry run, so an expired lab would keep billing: " +
			"set reaper_dry_run = false in ~/.lab/aws/baseline.tfvars and run lab cloud baseline apply")
	case s.Heartbeat == nil:
		return errors.New("the TTL reaper has never written a heartbeat: check its CloudWatch logs (/aws/lambda/" + ReaperFunction + ")")
	case s.Heartbeat.DryRun:
		return fmt.Errorf("the TTL reaper's last clean run (%s) was a dry run; wait for the next run (every 15 minutes)",
			s.Heartbeat.Time.Local().Format("15:04"))
	case now.Sub(s.Heartbeat.Time) > ReaperMaxAge:
		return fmt.Errorf("the TTL reaper's last run was %s ago (at %s), more than %s: check its CloudWatch logs (/aws/lambda/%s)",
			now.Sub(s.Heartbeat.Time).Round(time.Minute), s.Heartbeat.Time.Local().Format("15:04"), ReaperMaxAge, ReaperFunction)
	case s.Heartbeat.SelfTest == nil:
		return errors.New("the TTL reaper predates its self-test: run lab cloud baseline apply, then wait for the next run (every 15 minutes)")
	case !s.Heartbeat.SelfTest.OK() || !s.Heartbeat.Clean:
		errs := s.Heartbeat.Errors
		if len(errs) == 0 {
			errs = s.Heartbeat.SelfTest.Failures()
		}
		return fmt.Errorf("the TTL reaper's last run (at %s) was not clean, so it may not tear down an expired lab: %s; check its CloudWatch logs (/aws/lambda/%s)",
			s.Heartbeat.Time.Local().Format("15:04"), strings.Join(errs, "; "), ReaperFunction)
	case s.Heartbeat.ManualAction:
		return fmt.Errorf("an earlier cloud lab was not torn down after %d attempts (%s); clean it up first: "+
			"lab down --mode cloud, then lab cloud leftovers", reaper.MaxAttempts, s.Heartbeat.Teardown)
	}
	return nil
}

// GetReaperStatus reads the reaper's configuration and heartbeat.
func GetReaperStatus(ctx context.Context, cfg aws.Config, account string) (ReaperStatus, error) {
	fn, err := lambda.NewFromConfig(cfg).GetFunctionConfiguration(ctx, &lambda.GetFunctionConfigurationInput{
		FunctionName: aws.String(ReaperFunction)})
	if err != nil {
		return ReaperStatus{}, fmt.Errorf("reaper %s: %w", ReaperFunction, err)
	}
	st := ReaperStatus{DryRun: true}
	if fn.Environment != nil {
		if v, err := strconv.ParseBool(fn.Environment.Variables["DRY_RUN"]); err == nil {
			st.DryRun = v
		}
	}
	out, err := s3.NewFromConfig(cfg).GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(StateBucket(account)), Key: aws.String(reaper.HeartbeatKey)})
	var nsk *s3types.NoSuchKey
	switch {
	case errors.As(err, &nsk):
		return st, nil
	case err != nil:
		return st, fmt.Errorf("reaper heartbeat: %w", err)
	}
	defer func() { _ = out.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(out.Body, 64<<10))
	if err != nil {
		return st, err
	}
	var hb reaper.Heartbeat
	if err := json.Unmarshal(b, &hb); err != nil {
		return st, fmt.Errorf("reaper heartbeat: %w", err)
	}
	st.Heartbeat = &hb
	return st, nil
}

// TeardownProject is the baseline's teardown build.
const TeardownProject = "lab-teardown"

// RunningTeardown is the ID of a lab-teardown build in progress, or "".
func RunningTeardown(ctx context.Context, cfg aws.Config) (string, error) {
	id, err := reaper.CodeBuild{Client: codebuild.NewFromConfig(cfg), Project: TeardownProject}.Running(ctx)
	if err != nil {
		return "", fmt.Errorf("teardown builds: %w", err)
	}
	return id, nil
}

// TeardownBusy is the error while a teardown build runs: it may destroy
// what a lab up creates.
func TeardownBusy(id string) error {
	return fmt.Errorf("teardown build %s is running and may destroy what lab up creates; wait for it to finish "+
		"(log group /aws/codebuild/%s)", id, TeardownProject)
}

// PreflightCheck is everything lab up --mode cloud checks before it asks to
// apply: no teardown build is running, the reaper is armed and recent, and
// EKS has a Kubernetes version in standard support that the lab can use.
// It writes the version choice.
func (p *Provider) PreflightCheck(ctx context.Context, w io.Writer) error {
	id, err := p.teardown(ctx)
	if err != nil {
		return err
	}
	if id != "" {
		return fmt.Errorf("refusing to start a cloud lab: %w", TeardownBusy(id))
	}
	st, err := GetReaperStatus(ctx, p.cfg, p.account)
	if err != nil {
		return err
	}
	if err := st.Ready(p.now()); err != nil {
		return fmt.Errorf("refusing to start a cloud lab: %w", err)
	}
	_, err = p.ResolveVersion(ctx, w)
	return err
}
