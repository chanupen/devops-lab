package reaper

import (
	"context"
	"fmt"
	"sort"
)

// Leftovers is what a lab left behind once a direct lookup has checked
// the tagging API's answer: that API lists deleted resources for up to an
// hour.
type Leftovers struct {
	Left   []string `json:"left"`             // still there, or could not be checked
	Stale  []string `json:"stale"`            // listed by the tagging API, but terminated or not found
	Errors []string `json:"errors,omitempty"` // failed lookups; those resources are in Left
}

// SplitStale sorts resources into those that still exist and stale index
// entries, with the same Gone logic the reaper acts on. A failed lookup
// keeps the resource in left: a leftover is never hidden by an error.
func SplitStale(ctx context.Context, c Checker, rs []Resource) (left, stale []Resource, errs []string) {
	for _, r := range rs {
		live, err := c.Check(ctx, r.ARN)
		switch {
		case err != nil:
			errs = append(errs, fmt.Sprintf("check %s: %v", r.ARN, err))
			left = append(left, r)
		case live.Gone:
			stale = append(stale, r)
		default:
			left = append(left, r)
		}
	}
	return left, stale, errs
}

// FindLeftovers lists every lab=true resource outside the baseline and
// splits off the stale index entries. The teardown build asks for this
// (the Lambda's {"leftovers": true}) for its final report.
func FindLeftovers(ctx context.Context, l Lister, c Checker) (Leftovers, error) {
	all, err := l.List(ctx)
	if err != nil {
		return Leftovers{}, fmt.Errorf("list lab resources: %w", err)
	}
	var rs []Resource
	for _, r := range all {
		if r.Tags[ComponentTag] != "baseline" {
			rs = append(rs, r)
		}
	}
	left, stale, errs := SplitStale(ctx, c, rs)
	return Leftovers{Left: arns(left), Stale: arns(stale), Errors: errs}, nil
}

func arns(rs []Resource) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.ARN)
	}
	sort.Strings(out)
	return out
}
