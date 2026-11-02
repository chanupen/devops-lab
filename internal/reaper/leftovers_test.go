package reaper

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestFindLeftovers(t *testing.T) {
	vpc := arn("ec2", "vpc/vpc-0b010f23565bfeee6")
	ecr := arn("ecr", "repository/lab")
	all := append(slices.Clone(incident),
		expiredAs(vpc, "cluster"),
		expiredAs(ecr, "registry"),
		res(arn("s3", "lab-tofu-state-111122223333"), "lab", "true", "component", "baseline"))
	checker := goneAll
	checker.err = map[string]error{ecr: errors.New("Throttling")}
	got, err := FindLeftovers(context.Background(), fakeLister{rs: all}, checker)
	if err != nil {
		t.Fatal(err)
	}
	// The incident's three are stale; the VPC exists; the repository could
	// not be checked, so it stays a leftover; the baseline is never listed.
	if want := []string{vpc, ecr}; !slices.Equal(got.Left, want) {
		t.Errorf("left = %q, want %q", got.Left, want)
	}
	if want := []string{staleInstance, staleSG, staleVolume}; !slices.Equal(got.Stale, want) {
		t.Errorf("stale = %q, want %q", got.Stale, want)
	}
	if len(got.Errors) != 1 || !strings.Contains(got.Errors[0], "Throttling") {
		t.Errorf("errors = %q", got.Errors)
	}
	if _, err := FindLeftovers(context.Background(), fakeLister{err: errors.New("AccessDenied")}, checker); err == nil {
		t.Error("a failed listing must be an error, not 'nothing left'")
	}
}
