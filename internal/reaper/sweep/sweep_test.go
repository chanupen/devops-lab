package sweep

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/chanupen/devops-lab/internal/reaper"
)

func arn(service, resource string) string {
	return fmt.Sprintf("arn:aws:%s:ap-south-1:111122223333:%s", service, resource)
}

func res(a string, tags ...string) reaper.Resource {
	r := reaper.Resource{ARN: a, Tags: map[string]string{}}
	for i := 0; i+1 < len(tags); i += 2 {
		r.Tags[tags[i]] = tags[i+1]
	}
	return r
}

type fakeLister struct {
	rs  []reaper.Resource
	err error
}

func (f fakeLister) List(context.Context) ([]reaper.Resource, error) { return f.rs, f.err }

// fakeChecker: ARNs it does not know exist and carry the listed tags.
type fakeChecker struct {
	live map[string]reaper.Live
	err  map[string]error
}

func (f fakeChecker) Check(_ context.Context, arn string) (reaper.Live, error) {
	if err := f.err[arn]; err != nil {
		return reaper.Live{}, err
	}
	if l, ok := f.live[arn]; ok {
		return l, nil
	}
	return reaper.Live{Known: true}, nil
}

type fakeDeleter struct {
	got  []string
	wait map[string]bool
	fail map[string]bool
}

func (f *fakeDeleter) Delete(_ context.Context, t reaper.Target) error {
	f.got = append(f.got, string(t.Kind)+" "+t.ID)
	if f.wait[t.ID] {
		return fmt.Errorf("%w: in use", reaper.ErrNotYet)
	}
	if f.fail[t.ID] {
		return errors.New("AccessDenied")
	}
	return nil
}

func TestRun(t *testing.T) {
	const exp = "2026-11-06T10:12:00Z"
	env := map[string]string{OwnerTag: "me", reaper.ExpiresTag: exp}
	mine := func(a string) reaper.Resource { return res(a, "lab", "true", "owner", "me", "expires-at", exp) }
	secret := arn("secretsmanager", "secret:lab/sample-db/app-AbCdEf")
	vol := arn("ec2", "volume/vol-1")
	eni := arn("ec2", "network-interface/eni-1")

	tests := []struct {
		name     string
		rs       []reaper.Resource
		live     map[string]reaper.Live
		checkErr map[string]error
		wait     map[string]bool
		fail     map[string]bool
		want     []string // delete calls, in order
		deleted  int
		waiting  int
		errs     int
	}{
		{name: "nothing left"},
		{name: "deletes ENIs, then volumes, then secrets; a repeat is listed once",
			rs:   []reaper.Resource{mine(secret), mine(vol), mine(eni), mine(vol)},
			want: []string{"network-interface eni-1", "ebs-volume vol-1", "secret " + secret}, deleted: 3},
		{name: "other environments, the baseline and kinds outside the sweep stay",
			rs: []reaper.Resource{
				res(vol, "lab", "true", "owner", "me", "expires-at", "2026-11-05T14:12:00Z"), // an earlier lab: the reaper's
				res(secret, "lab", "true", "owner", "me"),                                    // baseline: no expires-at
				res(eni, "lab", "true", "owner", "someone", "expires-at", exp),
				mine(arn("ec2", "instance/i-1")),
				mine(arn("ec2", "security-group/sg-1")),
				mine(arn("ecr", "repository/lab/sample-db")),
				res(arn("ec2", "volume/vol-2"), "owner", "me", "expires-at", exp), // no lab=true
			}},
		{name: "gone resources are skipped",
			rs:   []reaper.Resource{mine(vol), mine(secret)},
			live: map[string]reaper.Live{secret: {Known: true, Gone: true}},
			want: []string{"ebs-volume vol-1"}, deleted: 1},
		{name: "live tags win over the tagging API's",
			rs: []reaper.Resource{mine(vol)},
			live: map[string]reaper.Live{vol: {Known: true,
				Tags: map[string]string{"lab": "true", "owner": "me", "expires-at": "2026-11-06T11:12:00Z"}}},
		},
		{name: "an in-use volume waits, a failed lookup or delete is an error, the rest still go",
			rs:       []reaper.Resource{mine(vol), mine(eni), mine(secret), mine(arn("ec2", "volume/vol-3"))},
			wait:     map[string]bool{"vol-1": true},
			fail:     map[string]bool{"vol-3": true},
			checkErr: map[string]error{eni: errors.New("throttled")},
			want:     []string{"ebs-volume vol-1", "ebs-volume vol-3", "secret " + secret}, deleted: 1, waiting: 1, errs: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &fakeDeleter{wait: tt.wait, fail: tt.fail}
			got, err := Run(context.Background(), fakeLister{rs: tt.rs}, fakeChecker{live: tt.live, err: tt.checkErr}, d, env, Kinds, t.Logf)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(d.got, tt.want) {
				t.Errorf("delete calls = %v, want %v", d.got, tt.want)
			}
			if len(got.Deleted) != tt.deleted || len(got.Waiting) != tt.waiting || len(got.Errors) != tt.errs {
				t.Errorf("deleted %d, waiting %v, errors %v; want %d, %d, %d",
					len(got.Deleted), got.Waiting, got.Errors, tt.deleted, tt.waiting, tt.errs)
			}
		})
	}
}

// Without the environment's owner and expires-at a sweep would match every
// lab=true resource, the baseline's included: it refuses before listing.
func TestRunNeedsTheEnvironment(t *testing.T) {
	vol := res(arn("ec2", "volume/vol-1"), "lab", "true", "owner", "me", "expires-at", "2026-11-06T10:12:00Z")
	for _, env := range []map[string]string{nil, {OwnerTag: "me"}, {reaper.ExpiresTag: "2026-11-06T10:12:00Z"}} {
		d := &fakeDeleter{}
		_, err := Run(context.Background(), fakeLister{rs: []reaper.Resource{vol}}, fakeChecker{}, d, env, Kinds, t.Logf)
		if err == nil || !strings.Contains(err.Error(), "needs the environment") {
			t.Errorf("env %v: err = %v", env, err)
		}
		if len(d.got) != 0 {
			t.Errorf("env %v: deleted %v", env, d.got)
		}
	}
}

func TestRunListError(t *testing.T) {
	env := map[string]string{OwnerTag: "me", reaper.ExpiresTag: "2026-11-06T10:12:00Z"}
	if _, err := Run(context.Background(), fakeLister{err: errors.New("denied")}, fakeChecker{}, &fakeDeleter{}, env, Kinds, t.Logf); err == nil {
		t.Error("want the lister's error")
	}
}

func TestListersMerge(t *testing.T) {
	a, b := arn("ec2", "volume/vol-1"), arn("ec2", "volume/vol-2")
	got, err := Listers{fakeLister{rs: []reaper.Resource{{ARN: a}}}, fakeLister{rs: []reaper.Resource{{ARN: b}}}}.List(context.Background())
	if err != nil || len(got) != 2 || got[0].ARN != a || got[1].ARN != b {
		t.Errorf("got %v, %v", got, err)
	}
	if _, err := (Listers{fakeLister{err: errors.New("denied")}}).List(context.Background()); err == nil {
		t.Error("want the lister's error")
	}
}

// The direct lister builds ARNs that kindOf parses back to the kind
// and ID its delete takes.
func TestListerARNs(t *testing.T) {
	l := Lister{Account: "111122223333", Region: "ap-south-1"}
	for _, c := range []struct {
		typ, id string
		kind    reaper.Kind
	}{{"volume", "vol-1", reaper.Volume}, {"network-interface", "eni-1", reaper.NetworkInterface}} {
		k, id, err := kindOf(l.ec2ARN(c.typ, c.id))
		if err != nil || k != c.kind || id != c.id {
			t.Errorf("%s/%s: %s %s %v", c.typ, c.id, k, id, err)
		}
	}
}
