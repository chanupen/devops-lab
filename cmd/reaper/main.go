// Command reaper is the lab TTL reaper Lambda. Every 15 minutes it deletes
// lab=true resources whose expires-at has passed. DRY_RUN=true (the
// default, and the baseline's default) only logs what it would delete.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"

	"github.com/chanupen/devops-lab/internal/reaper"
)

// dryRun is true unless DRY_RUN is set to a false value.
func dryRun() bool {
	v, err := strconv.ParseBool(os.Getenv("DRY_RUN"))
	return err != nil || v
}

func handle(ctx context.Context) (reaper.Result, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return reaper.Result{}, err
	}
	a := reaper.NewAWS(cfg)
	res, err := reaper.Run(ctx, a, a, time.Now().UTC(), dryRun(), log.Printf)
	if err != nil {
		return res, err
	}
	summary, _ := json.Marshal(map[string]any{"dryRun": res.DryRun, "deleted": len(res.Deleted), "kept": res.Kept,
		"skipped": len(res.Skipped), "errors": res.Errors})
	log.Printf("summary %s", summary)
	if len(res.Errors) > 0 {
		return res, fmt.Errorf("%d deletes failed", len(res.Errors))
	}
	return res, nil
}

func main() {
	log.SetFlags(0)
	lambda.Start(handle)
}
