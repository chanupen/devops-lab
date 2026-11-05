// Package drills runs break/fix drills on the main lab: each injects one
// fault into the sample app, times how long it takes to notice and to fix
// it, and puts everything back afterwards. A drill is a directory in
// drills/ with a drill.yaml, a README.md and four scripts; the format is
// generic, like the labs'.
package drills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// DefaultTimeLimit restores a drill nobody finished.
const DefaultTimeLimit = 30 * time.Minute

// Drill is one drill, loaded from its directory.
type Drill struct {
	ID     string   `yaml:"id"`
	Title  string   `yaml:"title"`
	Level  string   `yaml:"level"`
	Stacks []string `yaml:"stacks"` // main-lab stacks it needs
	// TimeLimit restores the lab on its own; 0 is DefaultTimeLimit.
	TimeLimit time.Duration `yaml:"timeLimit"`
	// Alert is the alert from the obs stack that should catch the fault.
	Alert Alert    `yaml:"alert"`
	Hints []string `yaml:"hints"`
	// Cause is the key in Causes of what really broke. lab drills ack
	// takes one of Causes; only Cause stops the detect timer. The cause
	// stays hidden until the drill ends, it is aborted, or the last hint.
	Cause  string            `yaml:"cause"`
	Causes map[string]string `yaml:"causes"`
	// Spoilers are words verify.sh must not print before recovery: they
	// would name the cause. The e2e test checks them.
	Spoilers []string `yaml:"spoilers"`
	// Touches are the namespaces whose objects the fault may change. The
	// e2e test compares their workloads before and after a drill.
	Touches []string `yaml:"touches"`
	// Snapshot are the objects the fault changes. lab saves them to the
	// state file before inject.sh runs and puts them back on restore.
	Snapshot []Ref `yaml:"snapshot"`

	Dir string `yaml:"-"`
	// Page is the README's intro: what the learner is told at the start.
	Page string `yaml:"-"`
}

// Alert names a Prometheus alert and the labels that tie it to the drill.
type Alert struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels"`
}

// Ref names one Kubernetes object.
type Ref struct {
	APIVersion string `yaml:"apiVersion"` // apps/v1, v1, argoproj.io/v1alpha1
	Resource   string `yaml:"resource"`   // plural: deployments, applications
	Namespace  string `yaml:"namespace"`
	Name       string `yaml:"name"`
}

func (r Ref) String() string {
	if r.Namespace == "" {
		return r.Resource + "/" + r.Name
	}
	return r.Resource + "/" + r.Namespace + "/" + r.Name
}

// CauseText describes the drill's cause.
func (d Drill) CauseText() string { return d.Causes[d.Cause] }

// CauseIDs are the keys of Causes, sorted, so their order gives nothing away.
func (d Drill) CauseIDs() []string {
	ids := make([]string, 0, len(d.Causes))
	for id := range d.Causes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// Limit is the drill's time limit.
func (d Drill) Limit() time.Duration {
	if d.TimeLimit <= 0 {
		return DefaultTimeLimit
	}
	return d.TimeLimit
}

// Script returns the path of one of the drill's scripts: inject, verify,
// restore or solution.
func (d Drill) Script(name string) string { return filepath.Join(d.Dir, name+".sh") }

// Scripts every drill has.
var scripts = []string{"inject", "verify", "restore", "solution"}

var idPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// LoadAll loads every drill under dir (one subdirectory each), in ID order.
func LoadAll(dir string) ([]Drill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Drill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "drill.yaml")); errors.Is(err, os.ErrNotExist) {
			continue
		}
		d, err := Load(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b Drill) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// Load reads one drill directory and checks that every file it needs is there.
func Load(dir string) (Drill, error) {
	var d Drill
	b, err := os.ReadFile(filepath.Join(dir, "drill.yaml"))
	if err != nil {
		return d, err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return d, fmt.Errorf("%s/drill.yaml: %w", dir, err)
	}
	if d.Dir, err = filepath.Abs(dir); err != nil {
		return d, err
	}
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		return d, err
	}
	d.Page = page(string(readme))
	return d, d.check()
}

// page is a README's intro: the text after the "# " title and before the
// first "## " section.
func page(md string) string {
	var out []string
	for i, l := range strings.Split(md, "\n") {
		if i == 0 && strings.HasPrefix(l, "# ") {
			continue
		}
		if strings.HasPrefix(l, "## ") {
			break
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func (d Drill) check() error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(d.Dir+": "+format, a...)) }
	if !idPattern.MatchString(d.ID) {
		bad("id %q is not words-with-dashes", d.ID)
	}
	if base := filepath.Base(d.Dir); base != d.ID {
		bad("id %q does not match the directory %s", d.ID, base)
	}
	if d.Title == "" || d.Level == "" {
		bad("drill.yaml needs title and level")
	}
	if len(d.Stacks) == 0 {
		bad("drill.yaml must list the stacks it needs")
	}
	if len(d.Hints) == 0 {
		bad("drill.yaml needs hints")
	}
	switch {
	case len(d.Causes) < 3:
		bad("drill.yaml needs causes: three or more suspects for lab drills ack")
	case d.Causes[d.Cause] == "":
		bad("cause %q is not one of causes", d.Cause)
	}
	for id := range d.Causes {
		if !idPattern.MatchString(id) {
			bad("cause %q is not words-with-dashes", id)
		}
	}
	if len(d.Touches) == 0 {
		bad("drill.yaml must list the namespaces it touches")
	}
	if d.Page == "" {
		bad("README.md needs an intro before its first \"## \" section: what the learner is told")
	}
	for _, r := range d.Snapshot {
		if r.APIVersion == "" || r.Resource == "" || r.Name == "" {
			bad("snapshot entry %+v needs apiVersion, resource and name", r)
		}
	}
	for _, s := range scripts {
		if _, err := os.Stat(d.Script(s)); err != nil {
			bad("%v", err)
		}
	}
	return errors.Join(errs...)
}
