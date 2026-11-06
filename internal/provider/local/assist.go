package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const assistRepo = "assist-receiver"

func assistSources(repo string) (map[string][]byte, string, error) {
	files := []string{"go.mod", "go.sum", "cmd/assist-receiver/Containerfile"}
	for _, dir := range []string{"cmd/assist-receiver", "internal/assist"} {
		entries, err := os.ReadDir(filepath.Join(repo, dir))
		if err != nil {
			return nil, "", err
		}
		for _, e := range entries {
			if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
	}
	sort.Strings(files)
	out := make(map[string][]byte, len(files))
	h := sha256.New()
	for _, name := range files {
		b, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			return nil, "", err
		}
		out[name] = b
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", name, len(b))
		_, _ = h.Write(b)
	}
	return out, hex.EncodeToString(h.Sum(nil))[:12], nil
}

// BuildAssist builds the receiver from a restricted source context and pushes it by digest.
func (p *Provider) BuildAssist(ctx context.Context, log io.Writer) (string, error) {
	repo := p.opts.RepoRoot
	if repo == "" {
		return "", fmt.Errorf("the assistant receiver image needs the lab repository")
	}
	files, tag, err := assistSources(repo)
	if err != nil {
		return "", fmt.Errorf("assistant receiver sources: %w", err)
	}
	bin := filepath.Join(repo, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(bin, "assist-context-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	for name, b := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			return "", err
		}
	}
	image := LabRegistryPush + "/" + assistRepo + ":" + tag
	logf(log, "building %s", image)
	if err := p.rt.BuildImage(ctx, dir, "cmd/assist-receiver/Containerfile", image, detail(log)); err != nil {
		return "", err
	}
	if err := p.rt.PushImage(ctx, image, false, detail(log)); err != nil {
		return "", err
	}
	digest, err := p.manifestDigest(ctx, assistRepo, tag)
	if err != nil {
		return "", err
	}
	ref := LabRegistryPull + "/" + assistRepo + "@" + digest
	logf(log, "assistant receiver image: %s", ref)
	return ref, nil
}

// AssistStale reports when the running receiver image differs from the sources.
func (p *Provider) AssistStale(ctx context.Context, deployed string) (string, error) {
	if p.opts.RepoRoot == "" {
		return "", nil
	}
	_, tag, err := assistSources(p.opts.RepoRoot)
	if err != nil {
		return "", err
	}
	digest, err := p.manifestDigest(ctx, assistRepo, tag)
	if err != nil || !strings.HasSuffix(deployed, "@"+digest) {
		return "assistant receiver differs from its sources", nil
	}
	return "", nil
}
