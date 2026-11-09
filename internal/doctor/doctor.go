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
	Name        string `json:"name"`
	Status      Status `json:"status"`
	Detail      string `json:"detail"`
	Fix         string `json:"fix,omitempty"`
	Unavailable bool   `json:"unavailable,omitempty"` // observational data could not be read
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
	// LabRegistry checks the host can push to the lab registry and the
	// cluster can pull from it; nil skips the check.
	LabRegistry func(ctx context.Context) RegistryState
	// MotoSecrets counts the secrets Moto holds (Secrets Manager
	// ListSecrets on 127.0.0.1:4566); nil checks only that it runs.
	MotoSecrets func(ctx context.Context) (int, error)
	// GHCR measures ghcr.io blob downloads; nil skips the check.
	GHCR GHCRProbe
	// AI reads the ai stack (Ollama through the cluster, Langfuse); nil
	// skips its checks.
	AI func(ctx context.Context) (AIState, error)
	// GitOps reads the root Argo CD Application.
	GitOps func(ctx context.Context) (GitOpsState, error)
}

// Unit is a part of the lab made of containers: a kind cluster or the
// image cache.
type Unit struct {
	Kind    string // "cluster" or "image cache"
	Name    string
	Nodes   int    // containers
	Running bool   // all of them
	Fix     string // the command that starts the stopped ones
	// Version is a running cluster's Kubernetes version from its node
	// ("v1.36.4"); VersionErr says why it could not be read.
	Version    string
	VersionErr string
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
	Memory  *Memory // reuse the memory preflight without reading it again
	// RequestedStacks adds host prerequisites without claiming pods are deployed.
	RequestedStacks []string
	// DeferClusterHealth skips live API checks during guided stopped-container recovery.
	// Ordinary doctor reports leave this false.
	DeferClusterHealth bool
	// K8sVersion is lab.yaml k8sVersion: the main cluster must run it.
	K8sVersion string
	// Stacks are the stacks deployed, from the state file.
	Stacks []string
	// ZotHosts are lab.yaml's zot.addHosts (name -> address in lab-zot).
	ZotHosts map[string]string
	// Drill is the drill still in the lab, from the state file; nil when
	// none is.
	Drill   *DrillState
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
		{"ollama in cluster", aiDeployed(checkOllamaInCluster)},
		{"ollama model", aiDeployed(checkOllamaModel)},
		{"langfuse", aiDeployed(checkLangfuse)},
		{"gitops revision", checkGitOpsRevision},
		{"lan listeners", checkLANListeners},
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
		{"lab registry", checkLabRegistry},
		{"moto", checkMoto},
		{"ghcr.io download", checkGHCR},
		{"drill", checkDrill},
		{"k8s support", checkK8sSupport},
	}
}

// DrillState is the active drill as lab doctor reports it.
type DrillState struct {
	Label    string // "drill dns", or "a random drill"
	Phase    string // injecting, running, restoring
	Deadline time.Time
	Error    string // the last restore error
	Now      time.Time
	// Fix replaces the default fix (an e2e test run's drill is aborted
	// with its own --state).
	Fix string
}

// checkDrill reports a drill whose fault is still in the lab. A running
// drill is a warning (the lab is broken on purpose); one past its time
// limit, or one whose inject or restore did not finish (a crash, a
// reboot), fails until lab drills abort or lab up puts it back.
func checkDrill(_ context.Context, _ System, opts Options) Result {
	d := opts.Drill
	if d == nil {
		return ok("none running")
	}
	fix := "lab drills abort (or lab up) puts back what the drill changed, from the state file"
	if d.Fix != "" {
		fix = d.Fix
	}
	switch {
	case d.Phase == "restoring":
		detail := d.Label + ": putting the lab back did not finish"
		if d.Error != "" {
			detail += ": " + firstLine(d.Error)
		}
		return fail(fix, "%s", detail)
	case d.Phase == "injecting":
		return fail(fix, "%s: its fault went in only partly (lab was interrupted)", d.Label)
	case !d.Now.Before(d.Deadline):
		return fail(fix, "%s: past its time limit (%s) and not restored yet", d.Label, d.Deadline.Local().Format("15:04"))
	}
	running := "lab drills status; lab drills verify or lab drills abort ends it"
	if d.Fix != "" {
		running = d.Fix
	}
	return warn(running, "%s is running: the sample app is broken on purpose until %s", d.Label, d.Deadline.Local().Format("15:04"))
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
