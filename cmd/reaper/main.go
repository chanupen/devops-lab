// Command reaper is the lab TTL reaper Lambda. Every 15 minutes it deletes
// controller-created lab=true resources whose expires-at has passed, and
// everything inside an expired lab VPC, and starts the teardown build for
// the tofu-managed rest (again while they remain, at most 6 times, then it
// emails "manual action needed"). DRY_RUN=true (the
// baseline's default) only logs. The teardown build invokes it with
// {"sweepOnly": true} to sweep before and after tofu destroy.
//
// After a run without errors it writes reaper/heartbeat.json to the state
// bucket; lab up --mode cloud refuses to start without a recent one.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
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
	env := reaper.Env{Lister: a, Deleter: a, VPCs: a}
	if p := os.Getenv("TEARDOWN_PROJECT"); p != "" {
		env.Teardown = reaper.CodeBuild{Client: codebuild.NewFromConfig(cfg), Project: p}
	}
	bucket := os.Getenv("HEARTBEAT_BUCKET")
	if bucket != "" {
		env.Store = reaper.S3Store{Client: s3.NewFromConfig(cfg), Bucket: bucket}
	}
	if t := os.Getenv("TEARDOWN_TOPIC"); t != "" {
		env.Notify = reaper.SNS{Client: sns.NewFromConfig(cfg), Topic: t}
	}
	now := time.Now().UTC()
	opts := reaper.Options{DryRun: dryRun(), SweepOnly: ev.SweepOnly}
	res, err := reaper.Run(ctx, env, now, opts, log.Printf)
	if err != nil {
		return res, err
	}
	summary, _ := json.Marshal(map[string]any{"dryRun": res.DryRun, "sweepOnly": ev.SweepOnly, "deleted": len(res.Deleted),
		"managed": len(res.Managed), "teardown": res.Teardown, "kept": res.Kept, "skipped": len(res.Skipped),
		"waiting": res.Waiting, "errors": res.Errors, "manualAction": res.ManualAction})
	log.Printf("summary %s", summary)
	if len(res.Errors) > 0 {
		return res, fmt.Errorf("%d errors: %v", len(res.Errors), res.Errors)
	}
	if bucket != "" && !ev.SweepOnly {
		hb, _ := json.Marshal(reaper.Heartbeat{Time: now, DryRun: res.DryRun, Deleted: len(res.Deleted),
			Managed: len(res.Managed), Waiting: len(res.Waiting), Teardown: res.Teardown, ManualAction: res.ManualAction})
		if _, err := s3.NewFromConfig(cfg).PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(reaper.HeartbeatKey),
			Body: bytes.NewReader(hb), ContentType: aws.String("application/json"),
		}); err != nil {
			return res, fmt.Errorf("write heartbeat: %w", err)
		}
	}
	return res, nil
}

func main() {
	log.SetFlags(0)
	lambda.Start(handle)
}
