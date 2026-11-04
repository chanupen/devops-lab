package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The sample-db app's image and its database credentials in Secrets
// Manager. The stack reads them through ESO (the ClusterSecretStore).
// Postgres's superuser has its own secret and is never rotated; the app
// logs in as app_v1, a member of the NOLOGIN group role app that holds the
// privileges, so a rotation adds app_v2 and retires app_v1 (lab 4).
const (
	SampleDBRepo        = "sample-db"
	SampleDBSecret      = "lab/sample-db/app" // {"username", "password"}
	SampleDBUser        = "app_v1"
	SampleDBAdminSecret = "lab/sample-db/admin"
	SampleDBAdminUser   = "postgres"
)

// BuildSampleDB builds the app from apps/sample-db through Podman's API,
// pushes it to the lab registry and returns how the cluster pulls it, by
// digest: lab-zot:5000/lab/sample-db@sha256:... The tag is a hash of the
// sources, so an unchanged app is not built under a new name; the build is
// reproducible (timestamps 0, pinned base images), so the digest holds.
// The cluster needs no registry credentials: Zot's lab/ namespace is open
// on the kind network only.
func (p *Provider) BuildSampleDB(ctx context.Context, log io.Writer) (string, error) {
	if p.opts.RepoRoot == "" {
		return "", fmt.Errorf("the sample-db image is built from the repo: run lab from it, or keep ~/.lab/lab.yaml a symlink into it")
	}
	dir := filepath.Join(p.opts.RepoRoot, "apps", "sample-db")
	sum, err := dirHash(dir)
	if err != nil {
		return "", fmt.Errorf("sample-db sources: %w", err)
	}
	tag := sum[:12]
	name := LabRegistryPush + "/" + SampleDBRepo + ":" + tag
	logf(log, "building %s from %s", name, dir)
	if err := p.rt.BuildImage(ctx, dir, "Containerfile", name, detail(log)); err != nil {
		return "", err
	}
	logf(log, "pushing %s", name)
	if err := p.rt.PushImage(ctx, name, false, detail(log)); err != nil {
		return "", err
	}
	digest, err := p.manifestDigest(ctx, SampleDBRepo, tag)
	if err != nil {
		return "", err
	}
	ref := LabRegistryPull + "/" + SampleDBRepo + "@" + digest
	logf(log, "sample-db image: %s", ref)
	return ref, nil
}

// SeedSampleDB creates the database's admin and app credentials in Moto
// unless they exist. Postgres takes them at its first start; the app reads
// its own from the Secret ESO makes.
func (p *Provider) SeedSampleDB(ctx context.Context, log io.Writer) error {
	if err := p.motoReady(ctx, log); err != nil {
		return err
	}
	for _, s := range []struct{ name, user string }{
		{SampleDBAdminSecret, SampleDBAdminUser}, {SampleDBSecret, SampleDBUser},
	} {
		created, err := SeedCredentials(ctx, p.secrets, s.name, s.user)
		if err != nil {
			return err
		}
		if created {
			logf(log, "created %s in %s with a random password", s.name, MotoContainer)
		}
	}
	return nil
}

// manifestDigest asks the lab registry for the digest of repo:tag.
func (p *Provider) manifestDigest(ctx context.Context, repo, tag string) (string, error) {
	_, ns, _ := strings.Cut(LabRegistryPush, "/") // "lab"
	u := p.registry + "/v2/" + ns + "/" + repo + "/manifests/" + tag
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close()
	d := resp.Header.Get("Docker-Content-Digest")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(d, "sha256:") {
		return "", fmt.Errorf("lab registry has no digest for %s:%s (HTTP %d)", repo, tag, resp.StatusCode)
	}
	return d, nil
}

// dirHash hashes the names and contents of dir's files (hidden ones left
// out, as the build context leaves them out).
func dirHash(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") && path != dir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		b, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", rel, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
