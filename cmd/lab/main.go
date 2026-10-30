// Command lab drives the DevOps lab: a TUI plus a CLI with the same actions.
package main

import (
	"errors"
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
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
