// Package labs runs the hands-on labs in labs/: each is a directory with a
// lab.yaml, a README.md split into "## Step N" sections, one read-only
// verify script per step, a reset script and reference solutions. The
// format is generic, so a lab can be copied into another repo as is.
package labs

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Needs values: where a lab's steps run.
const (
	NeedsScratch = "scratch" // its own single-node kind cluster, lab-scratch-<id>
	NeedsLab     = "lab"     // the main lab's cluster, with the stacks it lists
)

// Lab is one lab, loaded from its directory.
type Lab struct {
	ID     string   `yaml:"id"`
	Title  string   `yaml:"title"`
	Time   string   `yaml:"time"`  // how long it takes, e.g. "45m"
	Level  string   `yaml:"level"` // beginner, intermediate, advanced
	Modes  []string `yaml:"modes"` // local, cloud
	Needs  string   `yaml:"needs"` // scratch or lab
	Stacks []string `yaml:"stacks"`
	Tools  []string `yaml:"tools"` // commands the steps use, checked on start

	Dir   string `yaml:"-"`
	Intro string `yaml:"-"`
	Steps []Step `yaml:"-"`
}

// Step is one "## Step N: title" section of the README.
type Step struct {
	N        int
	Title    string
	Task     string   // what to do
	Hints    []string // shown one at a time
	Expected string   // what done looks like
}

// Number is the lab's number, the digits its ID starts with ("01").
func (l Lab) Number() string {
	n, _, _ := strings.Cut(l.ID, "-")
	return n
}

// VerifyScript is step n's verify script.
func (l Lab) VerifyScript(n int) string {
	return filepath.Join(l.Dir, "steps", fmt.Sprintf("%02d-verify.sh", n))
}

// SolveScript is step n's reference solution, a script that does the step.
func (l Lab) SolveScript(n int) string {
	return filepath.Join(l.Dir, "solution", fmt.Sprintf("%02d-solve.sh", n))
}

// ResetScript returns the lab's cluster to the lab's start.
func (l Lab) ResetScript() string { return filepath.Join(l.Dir, "reset.sh") }

// Step returns step n.
func (l Lab) Step(n int) (Step, bool) {
	if n < 1 || n > len(l.Steps) {
		return Step{}, false
	}
	return l.Steps[n-1], true
}

// LoadAll loads every lab under dir (one subdirectory each), in ID order.
func LoadAll(dir string) ([]Lab, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Lab
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "lab.yaml")); errors.Is(err, os.ErrNotExist) {
			continue
		}
		l, err := Load(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	slices.SortFunc(out, func(a, b Lab) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

var idPattern = regexp.MustCompile(`^[0-9]{2}-[a-z0-9]+(-[a-z0-9]+)*$`)

// Load reads one lab directory and checks that every file it needs is there.
func Load(dir string) (Lab, error) {
	var l Lab
	b, err := os.ReadFile(filepath.Join(dir, "lab.yaml"))
	if err != nil {
		return l, err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&l); err != nil {
		return l, fmt.Errorf("%s/lab.yaml: %w", dir, err)
	}
	l.Dir = dir
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		return l, err
	}
	l.Intro, l.Steps = ParseReadme(string(readme))
	return l, l.check()
}

func (l Lab) check() error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(l.Dir+": "+format, a...)) }
	if !idPattern.MatchString(l.ID) {
		bad("id %q is not NN-words-with-dashes", l.ID)
	}
	if base := filepath.Base(l.Dir); base != l.ID {
		bad("id %q does not match the directory %s", l.ID, base)
	}
	if l.Title == "" || l.Time == "" || l.Level == "" {
		bad("lab.yaml needs title, time and level")
	}
	if len(l.Modes) == 0 {
		bad("lab.yaml needs modes")
	}
	switch l.Needs {
	case NeedsScratch:
	case NeedsLab:
		if len(l.Stacks) == 0 {
			bad("needs: lab must list the stacks it needs")
		}
	default:
		bad("needs is %q, want scratch or lab", l.Needs)
	}
	if len(l.Steps) == 0 {
		bad("README.md has no \"## Step N: title\" sections")
	}
	for i, s := range l.Steps {
		if s.N != i+1 {
			bad("README step %d is numbered %d", i+1, s.N)
		}
		if strings.TrimSpace(s.Task) == "" || strings.TrimSpace(s.Expected) == "" || len(s.Hints) == 0 {
			bad("step %d needs a task, \"### Hints\" and \"### Expected result\"", s.N)
		}
		for _, f := range []string{l.VerifyScript(s.N), l.SolveScript(s.N)} {
			if _, err := os.Stat(f); err != nil {
				bad("step %d: %v", s.N, err)
			}
		}
	}
	if _, err := os.Stat(l.ResetScript()); err != nil {
		bad("%v", err)
	}
	return errors.Join(errs...)
}

var stepHeading = regexp.MustCompile(`^## Step ([0-9]+): (.+)$`)

// ParseReadme splits a lab README into the intro (everything before the
// first step, without the "# " title) and its steps. Inside a step,
// "### Hints" holds "- " bullets (one hint each; indented lines continue
// a hint) and "### Expected result" what done looks like; the text before
// either is the task. Fenced code blocks are kept as they are.
func ParseReadme(md string) (intro string, steps []Step) {
	var introLines []string
	var cur *Step
	section := "task"
	var task, expected []string
	flush := func() {
		if cur != nil {
			cur.Task = strings.TrimSpace(strings.Join(task, "\n"))
			cur.Expected = strings.TrimSpace(strings.Join(expected, "\n"))
			steps = append(steps, *cur)
		}
		task, expected = nil, nil
	}
	sc := bufio.NewScanner(strings.NewReader(md))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	fenced := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
		}
		if !fenced {
			if m := stepHeading.FindStringSubmatch(line); m != nil {
				flush()
				n, _ := strconv.Atoi(m[1])
				cur, section = &Step{N: n, Title: strings.TrimSpace(m[2])}, "task"
				continue
			}
			if cur != nil && strings.HasPrefix(line, "### ") {
				switch strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "### "))) {
				case "hints", "hint":
					section = "hints"
					continue
				case "expected result", "expected":
					section = "expected"
					continue
				}
			}
			if cur == nil && strings.HasPrefix(line, "# ") && len(introLines) == 0 {
				continue // the title
			}
		}
		switch {
		case cur == nil:
			introLines = append(introLines, line)
		case section == "task":
			task = append(task, line)
		case section == "expected":
			expected = append(expected, line)
		case section == "hints":
			switch {
			case strings.HasPrefix(line, "- "):
				cur.Hints = append(cur.Hints, strings.TrimPrefix(line, "- "))
			case len(cur.Hints) > 0 && (strings.HasPrefix(line, "  ") || fenced || strings.HasPrefix(strings.TrimSpace(line), "```")):
				cur.Hints[len(cur.Hints)-1] += "\n" + strings.TrimPrefix(line, "  ")
			}
		}
	}
	flush()
	return strings.TrimSpace(strings.Join(introLines, "\n")), steps
}
