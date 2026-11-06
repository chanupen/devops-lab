package engine

import (
	"context"
	"fmt"
	"io"
)

// Router sends each stack to the Runner that owns it: infrastructure stacks to
// the mode's provider, GitOps stacks to the GitOps runner.
type Router map[string]Runner

// Supports reports whether a runner is registered for the stack.
func (r Router) Supports(stack string) bool {
	_, ok := r[stack]
	return ok
}

// Apply implements Runner.
func (r Router) Apply(ctx context.Context, stack string, log io.Writer) error {
	run, ok := r[stack]
	if !ok {
		return fmt.Errorf("no runner for stack %q", stack)
	}
	return run.Apply(ctx, stack, log)
}

// Destroy implements Runner.
func (r Router) Destroy(ctx context.Context, stack string, log io.Writer) error {
	run, ok := r[stack]
	if !ok {
		return fmt.Errorf("no runner for stack %q", stack)
	}
	return run.Destroy(ctx, stack, log)
}

// Release implements Releaser: it calls the stack's runner if that runner
// keeps something outside the cluster, and does nothing otherwise.
func (r Router) Release(ctx context.Context, stack string, log io.Writer) error {
	run, ok := r[stack]
	if !ok {
		return fmt.Errorf("no runner for stack %q", stack)
	}
	if rel, ok := run.(Releaser); ok {
		return rel.Release(ctx, stack, log)
	}
	_, _ = fmt.Fprintln(log, "nothing outside the cluster to release")
	return nil
}
