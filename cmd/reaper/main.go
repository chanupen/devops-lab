// Command reaper is the lab TTL reaper Lambda. Every 15 minutes it deletes
// controller-created lab=true resources whose expires-at has passed, and
// everything inside an expired lab VPC, and starts the teardown build for
// the tofu-managed rest (again while they remain, at most 6 times, then it
// emails "manual action needed"). DRY_RUN=true (the
// baseline's default) only logs. The teardown build invokes it with
// {"sweepOnly": true} to sweep before and after tofu destroy.
//
// Each scheduled run also tests itself (it can read its teardown state and
// see the teardown project) and writes reaper/heartbeat.json to the state
// bucket, clean or not; lab up --mode cloud refuses unless the latest one
// is recent and clean. A run with any error returns it, so the async
// invocation fails and its on-failure destination (SNS lab-teardown)
// emails the owner.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sns"

	"github.com/chanupen/devops-lab/internal/reaper"
)

// Event is the Lambda input. The EventBridge schedule sends an empty one.
type Event struct {
	SweepOnly bool `json:"sweepOnly"`
}

// dryRun is true unless DRY_RUN is set to a false value.
func dryRun() bool {
	v, err := strconv.ParseBool(os.Getenv("DRY_RUN"))
	return err != nil || v
}

func handle(ctx context.Context, ev Event) (reaper.Result, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return reaper.Result{}, err
	}
	a := reaper.NewAWS(cfg)
	env := reaper.Env{Lister: a, Deleter: a, VPCs: a, Checker: a}
	if p := os.Getenv("TEARDOWN_PROJECT"); p != "" {
		env.Teardown = reaper.CodeBuild{Client: codebuild.NewFromConfig(cfg), Project: p}
	}
	bucket := os.Getenv("HEARTBEAT_BUCKET")
	if bucket != "" {
		env.Store = reaper.S3Store{Client: s3.NewFromConfig(cfg), Bucket: bucket}
		env.Stacks = reaper.S3Stacks{Client: s3.NewFromConfig(cfg), Bucket: bucket}
	}
	if t := os.Getenv("TEARDOWN_TOPIC"); t != "" {
		env.Notify = reaper.SNS{Client: sns.NewFromConfig(cfg), Topic: t}
	}
	var beat func(context.Context, []byte) error
	if bucket != "" {
		client := s3.NewFromConfig(cfg)
		beat = func(ctx context.Context, body []byte) error {
			_, err := client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(bucket), Key: aws.String(reaper.HeartbeatKey),
				Body: bytes.NewReader(body), ContentType: aws.String("application/json"),
			})
			return err
		}
	}
	return invoke(ctx, env, ev, reaper.Options{DryRun: dryRun(), SweepOnly: ev.SweepOnly}, time.Now().UTC(), beat, log.Printf)
}

// invoke is one Lambda run. It returns an error whenever the run was not
// clean: that error is what fires the on-failure destination. A scheduled
// run (not sweep-only) writes the heartbeat through beat, clean or not.
func invoke(ctx context.Context, env reaper.Env, ev Event, opts reaper.Options, now time.Time,
	beat func(context.Context, []byte) error, logf func(string, ...any)) (reaper.Result, error) {
	res, runErr := reaper.Run(ctx, env, now, opts, logf)
	errs := slices.Clone(res.Errors)
	if runErr != nil {
		errs = append(errs, runErr.Error())
	}
	var self *reaper.SelfTest
	if !ev.SweepOnly {
		st := reaper.CheckSelf(ctx, env)
		self = &st
		errs = append(errs, st.Failures()...)
	}
	summary, _ := json.Marshal(map[string]any{"dryRun": res.DryRun, "sweepOnly": ev.SweepOnly, "deleted": len(res.Deleted),
		"managed": len(res.Managed), "teardown": res.Teardown, "kept": res.Kept, "skipped": len(res.Skipped),
		"waiting": res.Waiting, "errors": errs, "manualAction": res.ManualAction, "selfTest": self})
	logf("summary %s", summary)
	if beat != nil && !ev.SweepOnly {
		hb, _ := json.Marshal(reaper.Heartbeat{Time: now, DryRun: res.DryRun, Deleted: len(res.Deleted),
			Managed: len(res.Managed), Waiting: len(res.Waiting), Teardown: res.Teardown, ManualAction: res.ManualAction,
			Clean: len(errs) == 0, Errors: errs, SelfTest: self})
		if err := beat(ctx, hb); err != nil {
			errs = append(errs, "write heartbeat: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return res, fmt.Errorf("%d errors: %v", len(errs), errs)
	}
	return res, nil
}

func main() {
	log.SetFlags(0)
	lambda.Start(handle)
}
