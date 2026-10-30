// Package config loads and validates lab.yaml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"go.yaml.in/yaml/v3"
)

// K8sVersion is the Kubernetes minor version the lab is pinned to. kind node
// images, EKS and kubectl skew checks all follow it.
const K8sVersion = "1.37"

// Allowed values, in documentation order.
var (
	Modes    = []string{"local", "cloud"}
	Profiles = []string{"minimal", "standard", "full"}
	GitOps   = []string{"argocd", "flux"}
)

// CloudRegion is the only AWS region the lab uses.
const CloudRegion = "ap-south-1"

// Config is the parsed lab.yaml.
type Config struct {
	Mode       string `yaml:"mode"`
	Profile    string `yaml:"profile"`
	GitOps     string `yaml:"gitops"`
	K8sVersion string `yaml:"k8sVersion"`
	Cloud      Cloud  `yaml:"cloud"`
}

// Cloud holds settings used only in cloud mode.
type Cloud struct {
	Region string   `yaml:"region"`
	TTL    Duration `yaml:"ttl"`
	Owner  string   `yaml:"owner"`
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
		Mode:       "local",
		Profile:    "standard",
		GitOps:     "argocd",
		K8sVersion: K8sVersion,
		Cloud:      Cloud{Region: CloudRegion, TTL: Duration(4 * time.Hour)},
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
	if c.K8sVersion != K8sVersion {
		errs = append(errs, fmt.Errorf("k8sVersion %q: the lab is pinned to %s", c.K8sVersion, K8sVersion))
	}
	if c.Cloud.Region != CloudRegion {
		errs = append(errs, fmt.Errorf("cloud.region %q: the lab only uses %s", c.Cloud.Region, CloudRegion))
	}
	if c.Cloud.TTL <= 0 {
		errs = append(errs, fmt.Errorf("cloud.ttl must be positive"))
	}
	if c.Mode == "cloud" && c.Cloud.Owner == "" {
		errs = append(errs, fmt.Errorf("cloud.owner is required in cloud mode (it becomes the owner tag)"))
	}
	return errors.Join(errs...)
}
