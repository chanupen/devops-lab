// Package config loads and validates lab.yaml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// K8sVersion is the default Kubernetes minor version of the local lab
// (lab.yaml k8sVersion): the newest the compatibility table allows in local
// mode, 1.36 because of Argo CD 3.5. The kind node image follows it, and
// lab up refuses a pin outside the table (internal/compat). The cloud lab
// picks its own (cloud.k8sVersion, else the newest EKS version in standard
// support that the pinned components support).
const K8sVersion = "1.36"

var minorVersion = regexp.MustCompile(`^1\.[0-9]{1,3}$`)

var hostName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// Allowed values, in documentation order.
var (
	Modes    = []string{"local", "cloud"}
	Profiles = []string{"minimal", "standard", "full"}
	GitOps   = []string{"argocd", "flux"}
)

// CloudRegion is the only AWS region the lab uses.
const CloudRegion = "ap-south-1"

// Cloud lab lifetimes: DefaultTTL unless lab.yaml or lab up --ttl says
// otherwise, and never more than MaxTTL from now, not even with lab extend.
const (
	DefaultTTL = 2 * time.Hour
	MaxTTL     = 8 * time.Hour
)

// Config is the parsed lab.yaml.
type Config struct {
	Mode    string `yaml:"mode"`
	Profile string `yaml:"profile"`
	// ExtraStacks are applied on top of the profile, e.g. obs on minimal.
	ExtraStacks []string `yaml:"extraStacks"`
	GitOps      string   `yaml:"gitops"`
	K8sVersion  string   `yaml:"k8sVersion"`
	Repo        Repo     `yaml:"repo"`
	Cloud       Cloud    `yaml:"cloud"`
	Zot         Zot      `yaml:"zot"`
}

// Zot holds settings for the local image cache (lab-zot).
type Zot struct {
	// AddHosts pins host names to addresses inside lab-zot only (Podman's
	// --add-host), for example a faster GitHub CDN address for
	// pkg-containers.githubusercontent.com when DNS returns a slow one
	// (lab doctor measures it). The host's /etc/hosts is never touched.
	AddHosts map[string]string `yaml:"addHosts"`
}

// Repo is the Git repository Argo CD (or Flux) syncs from. The lab always
// syncs its main branch, so there is no revision setting.
type Repo struct {
	URL string `yaml:"url"` // SSH URL when SSHKeyFile is set
	// SSHKeyFile is a read-only deploy key on this host. The gitops stack loads
	// it into the cluster as a Secret; it is never written to the repo.
	SSHKeyFile string `yaml:"sshKeyFile"`
}

// Cloud holds settings used only in cloud mode.
type Cloud struct {
	Region string   `yaml:"region"`
	TTL    Duration `yaml:"ttl"`
	Owner  string   `yaml:"owner"`
	// K8sVersion pins the EKS Kubernetes version. Empty (the default) lets
	// lab choose; a pin must still be in EKS standard support.
	K8sVersion string `yaml:"k8sVersion"`
}

// Duration is a time.Duration written as "4h" in YAML.
type Duration time.Duration

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: ttl %q: %w", n.Line, n.Value, err)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML writes the duration as a string.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Default returns the config used for fields lab.yaml leaves out.
func Default() Config {
	return Config{
		Mode:    "local",
		Profile: "minimal",
		// minimal + obs is what the local provider runs today; matches lab.yaml,
		// so lab run without a lab.yaml (ssh -t fedora lab) plans the same lab.
		ExtraStacks: []string{"obs"},
		GitOps:      "argocd",
		K8sVersion:  K8sVersion,
		Cloud:       Cloud{Region: CloudRegion, TTL: Duration(DefaultTTL)},
	}
}

// Load reads and validates the file at path.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg, err := Parse(b)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes YAML over the defaults and validates the result.
// Unknown keys are errors, so typos do not pass silently.
func Parse(b []byte) (Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate reports every invalid field at once.
func (c Config) Validate() error {
	var errs []error
	oneOf := func(field, v string, allowed []string) {
		if !slices.Contains(allowed, v) {
			errs = append(errs, fmt.Errorf("%s %q: want one of %v", field, v, allowed))
		}
	}
	oneOf("mode", c.Mode, Modes)
	oneOf("profile", c.Profile, Profiles)
	oneOf("gitops", c.GitOps, GitOps)
	for _, s := range c.ExtraStacks {
		if strings.TrimSpace(s) == "" {
			errs = append(errs, fmt.Errorf("extraStacks: empty stack name"))
		}
	}
	if !minorVersion.MatchString(c.K8sVersion) {
		errs = append(errs, fmt.Errorf("k8sVersion %q: want a minor version such as %q", c.K8sVersion, K8sVersion))
	}
	if v := c.Cloud.K8sVersion; v != "" && !minorVersion.MatchString(v) {
		errs = append(errs, fmt.Errorf("cloud.k8sVersion %q: want a minor version such as \"1.36\", or leave it out", v))
	}
	if c.Cloud.Region != CloudRegion {
		errs = append(errs, fmt.Errorf("cloud.region %q: the lab only uses %s", c.Cloud.Region, CloudRegion))
	}
	if err := CheckTTL(time.Duration(c.Cloud.TTL)); err != nil {
		errs = append(errs, fmt.Errorf("cloud.ttl: %w", err))
	}
	if c.Repo.SSHKeyFile != "" && !IsSSHURL(c.Repo.URL) {
		errs = append(errs, fmt.Errorf("repo.url %q: a deploy key needs an SSH URL such as git@github.com:owner/repo.git", c.Repo.URL))
	}
	for _, name := range slices.Sorted(maps.Keys(c.Zot.AddHosts)) {
		if !hostName.MatchString(name) {
			errs = append(errs, fmt.Errorf("zot.addHosts: %q is not a host name", name))
		}
		if net.ParseIP(c.Zot.AddHosts[name]) == nil {
			errs = append(errs, fmt.Errorf("zot.addHosts.%s: %q is not an IP address", name, c.Zot.AddHosts[name]))
		}
	}
	if c.Mode == "cloud" && c.Cloud.Owner == "" {
		errs = append(errs, fmt.Errorf("cloud.owner is required in cloud mode (it becomes the owner tag)"))
	}
	return errors.Join(errs...)
}

// ClusterK8sVersion is the Kubernetes version the mode's cluster runs, for
// plans and status: the local pin, or in cloud mode cloud.k8sVersion if set.
// Without a cloud pin the cloud provider chooses at apply time.
func (c Config) ClusterK8sVersion() string {
	switch {
	case c.Mode != "cloud":
		return c.K8sVersion
	case c.Cloud.K8sVersion != "":
		return c.Cloud.K8sVersion
	}
	return "chosen by lab (EKS standard support)"
}

// IsSSHURL reports whether url is an scp-style (git@host:path) or ssh:// URL.
func IsSSHURL(url string) bool {
	if strings.HasPrefix(url, "ssh://") {
		return true
	}
	user, rest, ok := strings.Cut(url, "@")
	return ok && user != "" && !strings.Contains(user, "/") && strings.Contains(rest, ":") && !strings.Contains(url, "://")
}

// CheckTTL accepts a cloud lab lifetime between one minute and MaxTTL.
func CheckTTL(d time.Duration) error {
	if d < time.Minute || d > MaxTTL {
		return fmt.Errorf("%s: want between 1m and %s", d, MaxTTL)
	}
	return nil
}
