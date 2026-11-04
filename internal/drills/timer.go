package drills

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"
)

// timerMargin is how many seconds after the deadline the timer fires.
const timerMargin = 10

// Timer restores the lab at a drill's deadline even when no lab process
// runs then.
type Timer interface {
	// Schedule makes run's restore happen at the deadline and returns a
	// name to cancel it by.
	Schedule(ctx context.Context, run string, at time.Time) (string, error)
	Cancel(ctx context.Context, name string) error
}

// SystemdTimer is a transient systemd user timer that runs
// "lab drills abort --expired --run <run>" at the deadline. The service
// retries every minute while another lab holds the lab lock (an open TUI
// restores an expired drill itself). A timer does not survive a reboot;
// after one, lab up restores the drill and lab doctor reports it.
type SystemdTimer struct {
	// Binary is the lab binary; Flags (--config, --state) follow
	// "drills abort --expired --run <run>".
	Binary string
	Flags  []string
	Now    func() time.Time
}

// Schedule implements Timer.
func (t SystemdTimer) Schedule(ctx context.Context, run string, at time.Time) (string, error) {
	unit := "lab-drill-" + strings.ToLower(run)
	now := time.Now
	if t.Now != nil {
		now = t.Now
	}
	// Round up and add a margin: a timer with AccuracySec may fire a few
	// seconds early, and abort --expired does nothing before the deadline.
	secs := max(int(math.Ceil(at.Sub(now()).Seconds())), 0) + timerMargin
	args := []string{"--user", "--unit=" + unit, "--collect",
		fmt.Sprintf("--on-active=%ds", secs), "--timer-property=AccuracySec=5s",
		"--property=Restart=on-failure", "--property=RestartSec=60",
		"--", t.Binary, "drills", "abort", "--expired", "--run", run}
	args = append(args, t.Flags...)
	out, err := exec.CommandContext(ctx, "systemd-run", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("systemd-run: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return unit, nil
}

// Cancel implements Timer. A timer that already fired or is gone is fine.
func (SystemdTimer) Cancel(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	_ = exec.CommandContext(ctx, "systemctl", "--user", "stop", name+".timer").Run()
	_ = exec.CommandContext(ctx, "systemctl", "--user", "reset-failed", name+".service").Run()
	return nil
}
