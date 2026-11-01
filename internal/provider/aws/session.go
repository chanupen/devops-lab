// Package aws is the cloud provider: AWS sessions (the lab profile assumes
// lab-admin with MFA), the account baseline run through OpenTofu, and the
// probes lab doctor --mode cloud uses.
package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Region is the only region the lab uses.
const Region = "ap-south-1"

// DefaultProfile is the CLI profile that assumes lab-admin.
const DefaultProfile = "lab"

// ErrNeedMFA is returned when the profile needs an MFA code and no prompt
// is available (for example lab doctor --json) and no cached session is
// valid. lab cloud login fixes it.
var ErrNeedMFA = errors.New("this profile needs an MFA code: run lab cloud login first")

// Session resolves credentials for one profile. For a profile that assumes
// a role with MFA it asks for the code once and caches the temporary keys
// under CacheDir (0600) until shortly before they expire, so later lab
// commands, and tofu, do not ask again.
type Session struct {
	Profile  string
	CacheDir string                                 // e.g. ~/.lab/aws
	Prompt   func(mfaSerial string) (string, error) // nil: never prompt
	Now      func() time.Time
}

// cached is the on-disk form of temporary credentials.
type cached struct {
	AccessKeyID     string    `json:"accessKeyId"`
	SecretAccessKey string    `json:"secretAccessKey"`
	SessionToken    string    `json:"sessionToken"`
	Expires         time.Time `json:"expires"`
}

// renewBefore is how long before expiry a cached session is replaced.
const renewBefore = 5 * time.Minute

func (s Session) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s Session) cachePath() string { return filepath.Join(s.CacheDir, s.Profile+".json") }

// Config returns an AWS config for the profile in the lab region, using
// the cached session when it is still valid.
func (s Session) Config(ctx context.Context) (aws.Config, error) {
	if c, ok := s.readCache(); ok {
		cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(Region),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, c.SessionToken)))
		return cfg, err
	}
	needed := false
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(Region), config.WithSharedConfigProfile(s.Profile),
		config.WithAssumeRoleCredentialOptions(func(o *stscreds.AssumeRoleOptions) {
			o.Duration = 4 * time.Hour
			o.TokenProvider = func() (string, error) {
				needed = true
				if s.Prompt == nil || o.SerialNumber == nil {
					return "", ErrNeedMFA
				}
				return s.Prompt(*o.SerialNumber)
			}
		}))
	if err != nil {
		return aws.Config{}, err
	}
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		if errors.Is(err, ErrNeedMFA) || (needed && s.Prompt == nil) {
			return aws.Config{}, ErrNeedMFA
		}
		return aws.Config{}, fmt.Errorf("credentials for profile %s: %w", s.Profile, err)
	}
	if creds.CanExpire {
		s.writeCache(cached{creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken, creds.Expires})
	}
	return cfg, nil
}

// Env is the environment tofu needs to use the same credentials. The
// profile itself is not passed: tofu cannot prompt for the MFA code.
func (s Session) Env(ctx context.Context, cfg aws.Config) (map[string]string, error) {
	c, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, err
	}
	env := map[string]string{
		"AWS_ACCESS_KEY_ID":     c.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY": c.SecretAccessKey,
		"AWS_REGION":            Region,
		"AWS_DEFAULT_REGION":    Region,
	}
	if c.SessionToken != "" {
		env["AWS_SESSION_TOKEN"] = c.SessionToken
	}
	return env, nil
}

// Identity returns the caller ARN and account.
func Identity(ctx context.Context, cfg aws.Config) (arn, account string, err error) {
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", "", err
	}
	return aws.ToString(out.Arn), aws.ToString(out.Account), nil
}

func (s Session) readCache() (cached, bool) {
	if s.CacheDir == "" {
		return cached{}, false
	}
	b, err := os.ReadFile(s.cachePath())
	if err != nil {
		return cached{}, false
	}
	var c cached
	if json.Unmarshal(b, &c) != nil || c.AccessKeyID == "" || !s.now().Add(renewBefore).Before(c.Expires) {
		return cached{}, false
	}
	return c, true
}

// writeCache is best effort: a failed write only means the next command
// asks for the MFA code again.
func (s Session) writeCache(c cached) {
	if s.CacheDir == "" {
		return
	}
	if err := os.MkdirAll(s.CacheDir, 0o700); err != nil {
		return
	}
	b, _ := json.Marshal(c)
	tmp := s.cachePath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.cachePath())
	}
}

// Forget removes the cached session, for example after a role change.
func (s Session) Forget() { _ = os.Remove(s.cachePath()) }
