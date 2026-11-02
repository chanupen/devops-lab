package reaper

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// fakeS3 answers like S3 does for a role with or without s3:ListBucket.
type fakeS3 struct {
	objects map[string][]byte
	// listPrefixes: ListObjectsV2 is allowed for prefixes under these;
	// none means the role has no s3:ListBucket at all.
	listPrefixes []string
	// get404: GetObject on a missing key says NoSuchKey. Without it S3
	// says 403 AccessDenied, as it does when the caller may not list.
	get404 bool
	puts   []string
}

func denied(action string) error {
	return &smithy.GenericAPIError{Code: "AccessDenied",
		Message: "User: arn:aws:sts::111122223333:assumed-role/lab-ttl-reaper/lab-ttl-reaper is not authorized to perform: " + action}
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	b, ok := f.objects[aws.ToString(in.Key)]
	switch {
	case ok:
		return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(b))}, nil
	case f.get404:
		return nil, &s3types.NoSuchKey{}
	}
	return nil, denied("s3:ListBucket")
}

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	b, _ := io.ReadAll(in.Body)
	if f.objects == nil {
		f.objects = map[string][]byte{}
	}
	f.objects[aws.ToString(in.Key)] = b
	f.puts = append(f.puts, aws.ToString(in.Key))
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	delete(f.objects, aws.ToString(in.Key))
	return &s3.DeleteObjectOutput{}, nil
}

func (f *fakeS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	prefix := aws.ToString(in.Prefix)
	if !slices.ContainsFunc(f.listPrefixes, func(p string) bool { return strings.HasPrefix(prefix, p) }) {
		return nil, denied("s3:ListBucket")
	}
	var out s3.ListObjectsV2Output
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out.Contents = append(out.Contents, s3types.Object{Key: aws.String(k)})
		}
	}
	return &out, nil
}

// The 2 Nov 2026 incident: the reaper's role had no s3:ListBucket, so S3
// answered 403 for the teardown state that did not exist yet, and the
// reaper never called StartBuild. Whatever S3 answers, the build starts.
func TestTeardownStartsWhateverS3Answers(t *testing.T) {
	lab := []Resource{expiredAs(arn("eks", "cluster/lab"), "cluster")}
	tests := []struct {
		name     string
		s3       *fakeS3
		attempt  int // what the build is told; 0 is "unknown"
		errors   int
		saved    bool // the state was written after the start
		teardown string
	}{
		{
			name:    "no s3:ListBucket, 403 for the missing state (the incident)",
			s3:      &fakeS3{},
			attempt: 0, errors: 1, saved: false,
			teardown: "started build lab-teardown:1 (attempt unknown: teardown state unreadable)",
		},
		{
			name:    "ListBucket on teardown/ turns the missing state into 404",
			s3:      &fakeS3{listPrefixes: []string{"teardown/"}, get404: true},
			attempt: 1, saved: true,
			teardown: "started build lab-teardown:1 (attempt 1 of 6)",
		},
		{
			name:    "S3 still says 403 on GetObject, but the prefix listing shows it is missing",
			s3:      &fakeS3{listPrefixes: []string{"teardown/"}},
			attempt: 1, saved: true,
			teardown: "started build lab-teardown:1 (attempt 1 of 6)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			td := &fakeTeardown{}
			r := run(t, Env{Lister: fakeLister{rs: lab}, Teardown: td, Store: S3Store{Client: tt.s3, Bucket: "state"}}, Options{})
			if td.starts != 1 {
				t.Fatalf("StartBuild called %d times, want 1 (errors %q)", td.starts, r.Errors)
			}
			if td.last != [2]int{tt.attempt, MaxAttempts} {
				t.Errorf("build told attempt %v, want %d", td.last, tt.attempt)
			}
			if r.Teardown != tt.teardown {
				t.Errorf("teardown = %q, want %q", r.Teardown, tt.teardown)
			}
			if len(r.Errors) != tt.errors {
				t.Errorf("errors = %q, want %d", r.Errors, tt.errors)
			}
			if got := slices.Contains(tt.s3.puts, TeardownStateKey); got != tt.saved {
				t.Errorf("state saved = %v, want %v", got, tt.saved)
			}
		})
	}
}

// A state the reaper cannot read is never overwritten: it may hold a
// higher attempt count or a manual-action flag.
func TestUnreadableStateIsNotOverwritten(t *testing.T) {
	store := &erringStore{err: errors.New("SlowDown")}
	td := &fakeTeardown{}
	r := run(t, Env{Lister: fakeLister{rs: []Resource{expiredAs(arn("eks", "cluster/lab"), "cluster")}}, Teardown: td, Store: store}, Options{})
	if td.starts != 1 || store.saves != 0 || len(r.Errors) != 1 || !strings.Contains(r.Errors[0], "SlowDown") {
		t.Errorf("%d starts, %d saves, errors %q", td.starts, store.saves, r.Errors)
	}
}

type erringStore struct {
	err   error
	saves int
}

func (e *erringStore) Load(context.Context) (TeardownState, error) { return TeardownState{}, e.err }
func (e *erringStore) Save(context.Context, TeardownState) error {
	e.saves++
	return nil
}
func (e *erringStore) Clear(context.Context) error { return nil }

func TestLoadReadsSavedState(t *testing.T) {
	f := &fakeS3{listPrefixes: []string{"teardown/"}}
	s := S3Store{Client: f, Bucket: "state"}
	want := TeardownState{Lab: "2026-11-02T11:00:00Z", Attempts: 3, LastBuild: "lab-teardown:3"}
	if err := s.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(context.Background())
	if err != nil || got.Lab != want.Lab || got.Attempts != 3 || got.LastBuild != want.LastBuild {
		t.Errorf("Load = %+v, %v", got, err)
	}
}

func TestCheckSelf(t *testing.T) {
	tests := []struct {
		name string
		env  Env
		ok   bool
		fail string
	}{
		{"both pass (no state yet counts as fine)",
			Env{Store: S3Store{Client: &fakeS3{listPrefixes: []string{"teardown/"}, get404: true}}, Teardown: &fakeTeardown{}}, true, ""},
		{"state unreadable: the incident",
			Env{Store: S3Store{Client: &fakeS3{}}, Teardown: &fakeTeardown{}}, false, "teardown state: api error AccessDenied"},
		{"project not visible",
			Env{Store: S3Store{Client: &fakeS3{get404: true}}, Teardown: &fakeTeardown{checkErr: errors.New("CodeBuild project lab-teardown not found")}},
			false, "teardown project: CodeBuild project lab-teardown not found"},
		{"nothing configured", Env{}, false, "no state bucket configured"},
	}
	for _, tt := range tests {
		st := CheckSelf(context.Background(), tt.env)
		if st.OK() != tt.ok {
			t.Errorf("%s: OK = %v (%+v)", tt.name, st.OK(), st)
		}
		if tt.fail != "" && !strings.Contains(strings.Join(st.Failures(), "\n"), tt.fail) {
			t.Errorf("%s: failures %q, want %q", tt.name, st.Failures(), tt.fail)
		}
	}
}
