// Package aws is the cloud provider: AWS sessions (the lab profile assumes
// lab-admin with MFA), OpenTofu modules (account baseline, ECR, EKS), the
// in-cluster pieces lab installs itself (Karpenter, the Load Balancer
// Controller), tags and TTL, and the probes lab doctor --mode cloud uses.
package aws

import (
	"context"
	"crypto/sha1" //nolint:gosec // the AWS CLI names its cache files by SHA-1; not a security use
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

// defaultDuration is the role session length when the profile sets none:
// lab-admin's maximum.
const defaultDuration = 4 * time.Hour

// ErrNeedMFA is returned when the profile needs an MFA code and no prompt
// is available (for example lab doctor --json) and no cached session is
// valid. lab cloud login fixes it.
var ErrNeedMFA = errors.New("this profile needs an MFA code: run lab cloud login first")

// ProfileError means the AWS SDK cannot resolve the profile from
// ~/.aws/config and ~/.aws/credentials: a missing profile, or a broken
// source_profile chain such as a profile that names itself. A cached role
// session does not help, because lab finds the cache file by the profile's
// role settings. lab never edits those files, so the error says what to
// check and what to run after the fix.
type ProfileError struct {
	Profile string
	Err     error
}

func (e *ProfileError) Error() string {
	return fmt.Sprintf("profile %s does not resolve in ~/.aws/config: %v", e.Profile, e.Err)
}

func (e *ProfileError) Unwrap() error { return e.Err }

// Fix is the next step to print under the error.
func (e *ProfileError) Fix() string {
	return fmt.Sprintf("check the profiles with: aws configure list --profile %s. "+
		"[profile %s] needs role_arn (lab-admin), source_profile and mfa_serial; the source profile "+
		"(devops) needs neither role_arn nor source_profile, its keys stay in ~/.aws/credentials. "+
		"lab never edits these files. Then run: lab cloud login", e.Profile, e.Profile)
}

// Session resolves credentials for one profile. For a profile that assumes
// a role it shares the AWS CLI's role cache: a session the aws CLI cached
// is used as is, and a session lab gets (asking for the MFA code once) is
// written where the CLI looks. So one MFA code serves lab, tofu, kubectl
// (through aws eks get-token) and the aws CLI until the session expires.
type Session struct {
	Profile  string
	CacheDir string                                 // the AWS CLI cache, ~/.aws/cli/cache
	Prompt   func(mfaSerial string) (string, error) // nil: never prompt
	Now      func() time.Time
	// Role reads the profile's role settings; nil reads ~/.aws/config.
	Role func(ctx context.Context, profile string) (Role, error)

	// configFiles and credentialsFiles replace the SDK's default shared
	// files when set (tests).
	configFiles, credentialsFiles []string
}

// Role is what the AWS CLI hashes into its cache file name.
type Role struct {
	ARN        string
	MFASerial  string
	ExternalID string
	Duration   time.Duration // 0 when the profile sets no duration_seconds
}

// DefaultCacheDir is the AWS CLI's role session cache.
func DefaultCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aws", "cli", "cache"), nil
}

// cliCache is the part of the AWS CLI's cache file that lab reads and
// writes: the AssumeRole response.
type cliCache struct {
	Credentials struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		SessionToken    string `json:"SessionToken"`
		Expiration      string `json:"Expiration"`
	} `json:"Credentials"`
}

// renewBefore is how long before expiry a cached session is replaced.
const renewBefore = 5 * time.Minute

func (s Session) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s Session) role(ctx context.Context) (Role, error) {
	if s.Role != nil {
		return s.Role(ctx, s.Profile)
	}
	sc, err := config.LoadSharedConfigProfile(ctx, s.Profile, func(o *config.LoadSharedConfigOptions) {
		if s.configFiles != nil {
			o.ConfigFiles, o.CredentialsFiles = s.configFiles, s.credentialsFiles
		}
	})
	if err != nil {
		return Role{}, &ProfileError{Profile: s.Profile, Err: err}
	}
	r := Role{ARN: sc.RoleARN, MFASerial: sc.MFASerial, ExternalID: sc.ExternalID}
	if sc.RoleDurationSeconds != nil {
		r.Duration = *sc.RoleDurationSeconds
	}
	return r, nil
}

// CacheKey is the AWS CLI's cache file name for an AssumeRole call: the
// SHA-1 of the call's arguments as Python's json.dumps(sort_keys=True)
// writes them, without the random session name.
func CacheKey(r Role) string {
	args := map[string]string{"RoleArn": strconv.Quote(r.ARN)}
	if r.MFASerial != "" {
		args["SerialNumber"] = strconv.Quote(r.MFASerial)
	}
	if r.ExternalID != "" {
		args["ExternalId"] = strconv.Quote(r.ExternalID)
	}
	if r.Duration > 0 {
		args["DurationSeconds"] = strconv.Itoa(int(r.Duration / time.Second))
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = strconv.Quote(k) + ": " + args[k]
	}
	sum := sha1.Sum([]byte("{" + strings.Join(parts, ", ") + "}")) //nolint:gosec // file name, see import
	return hex.EncodeToString(sum[:])
}

func (s Session) cachePath(r Role) string { return filepath.Join(s.CacheDir, CacheKey(r)+".json") }

// Config returns an AWS config for the profile in the lab region, using a
// cached role session when one is still valid.
func (s Session) Config(ctx context.Context) (aws.Config, error) {
	r, err := s.role(ctx)
	if err != nil {
		return aws.Config{}, err
	}
	if r.ARN == "" { // long-term keys, e.g. the devops profile
		return config.LoadDefaultConfig(ctx, config.WithRegion(Region), config.WithSharedConfigProfile(s.Profile))
	}
	if c, ok := s.readCache(r); ok {
		return config.LoadDefaultConfig(ctx, config.WithRegion(Region),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, c.SessionToken)))
	}
	needed := false
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(Region), config.WithSharedConfigProfile(s.Profile),
		config.WithAssumeRoleCredentialOptions(func(o *stscreds.AssumeRoleOptions) {
			o.Duration = defaultDuration
			if r.Duration > 0 {
				o.Duration = r.Duration
			}
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
		return aws.Config{}, fmt.Errorf("credentials for profile %s: %w (run lab cloud login for a fresh session)", s.Profile, err)
	}
	if creds.CanExpire {
		s.writeCache(r, creds)
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

type cachedCreds struct {
	AccessKeyID, SecretAccessKey, SessionToken string
	Expires                                    time.Time
}

func (s Session) readCache(r Role) (cachedCreds, bool) {
	if s.CacheDir == "" {
		return cachedCreds{}, false
	}
	b, err := os.ReadFile(s.cachePath(r))
	if err != nil {
		return cachedCreds{}, false
	}
	var c cliCache
	if json.Unmarshal(b, &c) != nil || c.Credentials.AccessKeyID == "" {
		return cachedCreds{}, false
	}
	exp, err := time.Parse(time.RFC3339, c.Credentials.Expiration)
	if err != nil || !s.now().Add(renewBefore).Before(exp) {
		return cachedCreds{}, false
	}
	return cachedCreds{c.Credentials.AccessKeyID, c.Credentials.SecretAccessKey, c.Credentials.SessionToken, exp}, true
}

// writeCache stores the session in the CLI's format. It is best effort: a
// failed write only means the next command asks for the MFA code again.
func (s Session) writeCache(r Role, creds aws.Credentials) {
	if s.CacheDir == "" {
		return
	}
	if err := os.MkdirAll(s.CacheDir, 0o700); err != nil {
		return
	}
	var c cliCache
	c.Credentials.AccessKeyID = creds.AccessKeyID
	c.Credentials.SecretAccessKey = creds.SecretAccessKey
	c.Credentials.SessionToken = creds.SessionToken
	c.Credentials.Expiration = creds.Expires.UTC().Format("2006-01-02T15:04:05-07:00")
	b, _ := json.Marshal(c)
	tmp := s.cachePath(r) + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.cachePath(r))
	}
}

// Forget removes the cached session, so the next command asks for a code.
func (s Session) Forget(ctx context.Context) error {
	r, err := s.role(ctx)
	if err != nil || r.ARN == "" || s.CacheDir == "" {
		return err
	}
	if err := os.Remove(s.cachePath(r)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Expires reports when the cached role session ends, if there is one.
func (s Session) Expires(ctx context.Context) (time.Time, bool) {
	r, err := s.role(ctx)
	if err != nil || r.ARN == "" {
		return time.Time{}, false
	}
	c, ok := s.readCache(r)
	return c.Expires, ok
}
