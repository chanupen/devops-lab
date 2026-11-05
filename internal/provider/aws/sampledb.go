package aws

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// The sample-db stack in cloud mode: its image in ECR, built for the arm64
// nodes, and its database credentials in AWS Secrets Manager, where ESO
// reads them with EKS Pod Identity. The names are the local ones, so the
// stack's chart and lab 4 do not change between modes.
const (
	SampleDBRepo        = "sample-db"
	SampleDBSecret      = "lab/sample-db/app"
	SampleDBUser        = "app_v1"
	SampleDBAdminSecret = "lab/sample-db/admin"
	SampleDBAdminUser   = "postgres"
	// NodePlatform is what the cloud nodes run (Graviton, arm64).
	NodePlatform = "linux/arm64"
)

// ImageBuilder builds and pushes images: the host's rootless Podman.
type ImageBuilder interface {
	BuildImageFor(ctx context.Context, dir, containerfile, tag, platform string, log io.Writer) error
	PushImageAuth(ctx context.Context, name, username, password string, log io.Writer) error
}

// LabRegistry is the cloud lab registry, <account>.dkr.ecr.ap-south-1.amazonaws.com/lab:
// LAB_REGISTRY_PUSH and LAB_REGISTRY_PULL both, since the host and the
// nodes reach ECR at the same name.
func (p *Provider) LabRegistry() string { return LabRegistryFor(p.account) }

// LabRegistryFor is the lab registry in account.
func LabRegistryFor(account string) string {
	return fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/lab", account, Region)
}

// LabRegistryEnv is the labs' registry environment in cloud mode.
func (p *Provider) LabRegistryEnv() []string {
	r := p.LabRegistry()
	return []string{"LAB_REGISTRY_PUSH=" + r, "LAB_REGISTRY_PULL=" + r}
}

// BuildSampleDB builds the app in dir for the nodes' platform, pushes it to
// ECR as <registry>/sample-db:tag and returns it by digest. tag is a hash
// of the sources, as in local mode, so an unchanged app is pushed once.
func (p *Provider) BuildSampleDB(ctx context.Context, b ImageBuilder, dir, tag string, log io.Writer) (string, error) {
	name := p.LabRegistry() + "/" + SampleDBRepo + ":" + tag
	_, _ = fmt.Fprintf(log, "building %s (%s) from %s\n", name, NodePlatform, dir)
	if err := b.BuildImageFor(ctx, dir, "Containerfile", name, NodePlatform, log); err != nil {
		return "", err
	}
	user, pass, err := ecrLogin(ctx, ecr.NewFromConfig(p.cfg))
	if err != nil {
		return "", err
	}
	_, _ = fmt.Fprintf(log, "pushing %s\n", name)
	if err := b.PushImageAuth(ctx, name, user, pass, log); err != nil {
		return "", err
	}
	digest, err := imageDigest(ctx, ecr.NewFromConfig(p.cfg), "lab/"+SampleDBRepo, tag)
	if err != nil {
		return "", err
	}
	ref := p.LabRegistry() + "/" + SampleDBRepo + "@" + digest
	_, _ = fmt.Fprintf(log, "sample-db image: %s\n", ref)
	return ref, nil
}

// ecrLogin returns the registry login from an ECR authorization token
// (valid 12 hours, never written to disk).
func ecrLogin(ctx context.Context, c *ecr.Client) (string, string, error) {
	out, err := c.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return "", "", fmt.Errorf("ECR login: %w", err)
	}
	if len(out.AuthorizationData) == 0 {
		return "", "", errors.New("ECR login: no authorization data")
	}
	raw, err := base64.StdEncoding.DecodeString(aws.ToString(out.AuthorizationData[0].AuthorizationToken))
	if err != nil {
		return "", "", fmt.Errorf("ECR login: %w", err)
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", "", errors.New("ECR login: malformed token")
	}
	return user, pass, nil
}

func imageDigest(ctx context.Context, c *ecr.Client, repo, tag string) (string, error) {
	out, err := c.DescribeImages(ctx, &ecr.DescribeImagesInput{RepositoryName: aws.String(repo),
		ImageIds: []ecrtypes.ImageIdentifier{{ImageTag: aws.String(tag)}}})
	if err != nil {
		return "", fmt.Errorf("ECR %s:%s: %w", repo, tag, err)
	}
	if len(out.ImageDetails) == 0 || !strings.HasPrefix(aws.ToString(out.ImageDetails[0].ImageDigest), "sha256:") {
		return "", fmt.Errorf("ECR has no digest for %s:%s", repo, tag)
	}
	return aws.ToString(out.ImageDetails[0].ImageDigest), nil
}

// Secrets is AWS Secrets Manager as the lab uses it.
type Secrets interface {
	// Get returns the secret's current value; ok is false when there is none.
	Get(ctx context.Context, name string) (value string, ok bool, err error)
	// Create makes the secret with the lab tags.
	Create(ctx context.Context, name, value string, tags map[string]string) error
	// Delete removes the secret at once (no recovery window, so nothing is
	// left billed); a missing secret is not an error.
	Delete(ctx context.Context, name string) error
}

// NewSecrets is Secrets on cfg.
func NewSecrets(cfg aws.Config) Secrets { return smSecrets{secretsmanager.NewFromConfig(cfg)} }

type smSecrets struct{ c *secretsmanager.Client }

func (s smSecrets) Get(ctx context.Context, name string) (string, bool, error) {
	out, err := s.c.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(name)})
	var nf *smtypes.ResourceNotFoundException
	var inv *smtypes.InvalidRequestException
	switch {
	case errors.As(err, &nf):
		return "", false, nil
	case errors.As(err, &inv) && strings.Contains(inv.ErrorMessage(), "marked for deletion"):
		return "", false, fmt.Errorf("secret %s is scheduled for deletion: restore it (aws secretsmanager restore-secret) or delete it with --force-delete-without-recovery", name)
	case err != nil:
		return "", false, err
	}
	return aws.ToString(out.SecretString), true, nil
}

func (s smSecrets) Create(ctx context.Context, name, value string, tags map[string]string) error {
	in := &secretsmanager.CreateSecretInput{Name: aws.String(name), SecretString: aws.String(value)}
	for k, v := range tags {
		in.Tags = append(in.Tags, smtypes.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	_, err := s.c.CreateSecret(ctx, in)
	return err
}

func (s smSecrets) Delete(ctx context.Context, name string) error {
	_, err := s.c.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{SecretId: aws.String(name),
		ForceDeleteWithoutRecovery: aws.Bool(true)})
	var nf *smtypes.ResourceNotFoundException
	if errors.As(err, &nf) {
		return nil
	}
	return err
}

// SampleDBSecrets are the sample-db stack's secrets and their users.
var SampleDBSecrets = []struct{ Name, User string }{
	{SampleDBAdminSecret, SampleDBAdminUser}, {SampleDBSecret, SampleDBUser},
}

// SeedSampleDB creates the database's admin and app credentials in Secrets
// Manager unless they exist, with the lab tags (component sample-db), so
// lab extend retags them and the reaper deletes them after expires-at.
func (p *Provider) SeedSampleDB(ctx context.Context, log io.Writer) error {
	exp, err := p.expiresAt(true)
	if err != nil {
		return err
	}
	return SeedSampleDB(ctx, p.secrets, SampleDBTags(p.opts.Owner, exp), log)
}

// SampleDBTags are the tags of the sample-db stack's secrets.
func SampleDBTags(owner string, exp time.Time) map[string]string {
	t := LabTags(owner, exp)
	t["component"] = "sample-db"
	return t
}

// SeedSampleDB creates each of SampleDBSecrets with a random password
// unless it exists.
func SeedSampleDB(ctx context.Context, s Secrets, tags map[string]string, log io.Writer) error {
	for _, sec := range SampleDBSecrets {
		if _, ok, err := s.Get(ctx, sec.Name); err != nil {
			return err
		} else if ok {
			continue
		}
		b := make([]byte, 18)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		v, err := json.Marshal(map[string]string{"username": sec.User, "password": hex.EncodeToString(b)})
		if err != nil {
			return err
		}
		if err := s.Create(ctx, sec.Name, string(v), tags); err != nil {
			return fmt.Errorf("create %s: %w", sec.Name, err)
		}
		_, _ = fmt.Fprintf(log, "created %s in Secrets Manager with a random password\n", sec.Name)
	}
	return nil
}

// RemoveSampleDB deletes the sample-db stack's secrets, without a recovery
// window: the stack's database goes with them.
func (p *Provider) RemoveSampleDB(ctx context.Context, log io.Writer) error {
	return RemoveSampleDB(ctx, p.secrets, log)
}

// RemoveSampleDB deletes each of SampleDBSecrets.
func RemoveSampleDB(ctx context.Context, s Secrets, log io.Writer) error {
	var errs []error
	for _, sec := range SampleDBSecrets {
		if err := s.Delete(ctx, sec.Name); err != nil {
			errs = append(errs, fmt.Errorf("delete %s: %w", sec.Name, err))
			continue
		}
		_, _ = fmt.Fprintf(log, "deleted %s from Secrets Manager (no recovery window)\n", sec.Name)
	}
	return errors.Join(errs...)
}
