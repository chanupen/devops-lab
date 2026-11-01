// Command lab drives the DevOps lab: a TUI plus a CLI with the same actions.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Must stay imported: it stops Bubble Tea's start-up terminal query.
	_ "github.com/chanupen/devops-lab/cmd/lab/internal/termbg"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	startTrace(time.Now())
	// Ctrl-C cancels the context: the engine stops before the next step.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		var exit exitError
		if errors.As(err, &exit) {
			os.Exit(exit.code)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// exitError carries a non-zero exit code without printing an extra message.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }
