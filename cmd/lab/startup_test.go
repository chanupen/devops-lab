//go:build linux || darwin

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/creack/pty"
)

// startupChildEnv marks the child process of TestNoStartupTerminalQuery.
const startupChildEnv = "LAB_TEST_STARTUP_CHILD"

// Bubble Tea v1 queries the terminal's background colour (OSC 11) in its
// package init and waits up to 5 s for a reply. Package termbg stops that.
// This test checks the behaviour, not how termbg achieves it: it re-runs this
// test binary on a pty that never answers, which runs the same package init
// as lab, then checks that no query was sent and that the child exited well
// within the 5 s timeout.
func TestNoStartupTerminalQuery(t *testing.T) {
	if os.Getenv(startupChildEnv) == "1" {
		return // package init has run; exit at once
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestNoStartupTerminalQuery$")
	cmd.Env = append(os.Environ(), startupChildEnv+"=1", "TERM=xterm-256color")
	// pty.Start makes the pty the child's controlling terminal and puts the
	// child in its foreground, which termenv requires before it queries.
	f, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer func() { _ = f.Close() }()

	start := time.Now()
	out := make(chan []byte, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, f) // returns when the child closes the pty
		out <- b.Bytes()
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("child: %v", err)
		}
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child still running after 3s: start-up is waiting on the terminal")
	}
	elapsed := time.Since(start)
	_ = f.Close()
	got := <-out

	if bytes.Contains(got, []byte("\x1b]11;?")) {
		t.Errorf("start-up sent an OSC 11 background query to the terminal: %q", got)
	}
	if elapsed > 2*time.Second {
		t.Errorf("child took %s; a query waiting on the terminal takes 5s", elapsed)
	}
}
