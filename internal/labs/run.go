package labs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// VerifyTimeout is how long a verify script may run.
const VerifyTimeout = 60 * time.Second

// Runner runs a lab script. ExecRunner runs it with bash; tests use a fake.
type Runner interface {
	// Run runs script in dir with env added to the environment and returns
	// its combined output. A non-zero exit is an *exec.ExitError.
	Run(ctx context.Context, script, dir string, env []string) (string, error)
}

// ExecRunner runs scripts with bash, in their own process group, so a
// timeout also stops what they started (a kubectl port-forward).
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, script, dir string, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Result is one verify run.
type Result struct {
	Step     int
	Passed   bool
	Reason   string // why not, in one line; empty when it passed
	Output   string // everything the script printed
	Duration time.Duration
}

// verify runs step n's verify script with KUBECONFIG set and a timeout.
func verify(ctx context.Context, r Runner, l Lab, n int, kubeconfig string, timeout time.Duration) Result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	out, err := r.Run(ctx, l.VerifyScript(n), l.Dir, scriptEnv(l, n, kubeconfig))
	res := Result{Step: n, Output: out, Duration: time.Since(start), Passed: err == nil}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.Reason = fmt.Sprintf("timed out after %s", timeout)
	case errors.As(err, &exit):
		res.Reason = lastLine(out)
		if res.Reason == "" {
			res.Reason = fmt.Sprintf("exit status %d", exit.ExitCode())
		}
	default:
		res.Reason = err.Error()
	}
	return res
}

func scriptEnv(l Lab, n int, kubeconfig string) []string {
	return []string{"KUBECONFIG=" + kubeconfig, "LAB_ID=" + l.ID, "LAB_DIR=" + l.Dir, fmt.Sprintf("LAB_STEP=%d", n)}
}

// lastLine is the script's last non-empty line: verify scripts print why a
// step is not done as their last line.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}
