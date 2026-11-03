// Package doctor checks that the host is ready to run the lab.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Status is the outcome of a single check.
type Status int

// Check outcomes. Only Fail makes `lab doctor` exit non-zero.
const (
	OK      Status = iota // check passed
	Warn                  // works, but not as recommended
	Fail                  // blocks the lab
	Skip                  // could not run; not counted as a failure
	Stopped               // a lab container is there but stopped (a reboot); not a failure
)

func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Warn:
		return "warn"
	case Fail:
		return "fail"
	case Stopped:
		return "stopped"
	default:
		return "skip"
	}
}

// MarshalJSON writes the status as its name.
func (s Status) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// Result is what a check reports.
type Result struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// System is the host surface the checks read from. Tests replace it with fakes.
type System struct {
	UID      int
	ReadFile func(path string) ([]byte, error)
	Exists   func(path string) bool
	LookPath func(name string) (string, error)
	Run      func(ctx context.Context, name string, args ...string) (string, error)
	HTTPGet  func(ctx context.Context, url string) (int, []byte, error)
	// Units lists the lab's containers (cluster nodes, the image cache);
	// nil skips those checks.
	Units func(ctx context.Context) ([]Unit, error)
}

// Unit is a part of the lab made of containers: a kind cluster or the
// image cache.
type Unit struct {
	Kind    string // "cluster" or "image cache"
	Name    string
	Nodes   int    // containers
	Running bool   // all of them
	Fix     string // the command that starts the stopped ones
}

// HostSystem returns a System backed by the real host.
func HostSystem() System {
	return System{
		UID:      os.Getuid(),
		ReadFile: os.ReadFile,
		Exists: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
		LookPath: exec.LookPath,
		Run: func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			return strings.TrimSpace(string(out)), err
		},
		HTTPGet: func(ctx context.Context, url string) (int, []byte, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return 0, nil, err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return 0, nil, err
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			return resp.StatusCode, body, err
		},
	}
}

// Options select what the checks compare against.
type Options struct {
	Profile string
	Timeout time.Duration // per check
}

// Check is one named host check.
type Check struct {
	Name string
	Run  func(ctx context.Context, sys System, opts Options) Result
}

// Checks returns every check in display order.
func Checks() []Check {
	return []Check{
		{"podman", checkPodman},
		{"podman.socket", checkPodmanSocket},
		{"cgroup delegation", checkCgroups},
		{"inotify", checkInotify},
		{"iptables modules", checkIPTables},
		{"selinux", checkSELinux},
		{"nvidia driver", aiCheck(checkNvidiaDriver)},
		{"gpu vram", aiCheck(checkVRAM)},
		{"nvidia cdi", aiCheck(checkCDI)},
		{"ollama", aiCheck(checkOllama)},
		{"go", toolCheck("go")},
		{"kind", toolCheck("kind")},
		{"kubectl", toolCheck("kubectl")},
		{"helm", toolCheck("helm")},
		{"k9s", toolCheck("k9s")},
		{"tofu/terraform", checkIaC},
		{"aws", toolCheck("aws")},
		{"memory", checkMemory},
		{"lab cluster", unitCheck("cluster", "lab")},
		{"scratch clusters", checkScratchClusters},
		{"image cache", unitCheck("image cache", "lab-zot")},
	}
}

// Run executes the checks concurrently and returns results in input order.
func Run(ctx context.Context, sys System, opts Options, checks []Check) []Result {
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Second
	}
	results := make([]Result, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
			r := c.Run(cctx, sys, opts)
			r.Name = c.Name
			results[i] = r
		}()
	}
	wg.Wait()
	return results
}

// Failed reports whether any result failed.
func Failed(results []Result) bool {
	for _, r := range results {
		if r.Status == Fail {
			return true
		}
	}
	return false
}

func ok(format string, a ...any) Result { return Result{Status: OK, Detail: fmt.Sprintf(format, a...)} }

func warn(fix, format string, a ...any) Result {
	return Result{Status: Warn, Detail: fmt.Sprintf(format, a...), Fix: fix}
}

func fail(fix, format string, a ...any) Result {
	return Result{Status: Fail, Detail: fmt.Sprintf(format, a...), Fix: fix}
}
