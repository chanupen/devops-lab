package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Remediation describes a command to print, never to execute.
type Remediation struct {
	Command          string `json:"command"`
	RequiresSudo     bool   `json:"requiresSudo"`
	Effect           string `json:"effect"`
	Explanation      string `json:"explanation"`
	Manual           bool   `json:"manual"`
	RequiresApproval bool   `json:"requiresApproval"`
	lab              bool
	kube             bool
	profile          bool
}

func repair(command, explanation, effect string, sudo bool) Remediation {
	return Remediation{Command: command, Explanation: explanation, Effect: effect,
		RequiresSudo: sudo, RequiresApproval: effect != "read-only"}
}

func inspection(command, explanation string) Remediation {
	return Remediation{Command: command, Explanation: explanation, Effect: "read-only", Manual: true}
}

func labInspection(command, explanation string) Remediation {
	r := inspection(command, explanation)
	r.lab = true
	return r
}

func kubeInspection(command, explanation string) Remediation {
	r := inspection(command, explanation)
	r.kube = true
	return r
}

func withRemediation(r Result, fixes ...Remediation) Result {
	r.Remediation = fixes
	return r
}

// inspect supplies an exact diagnostic when a check cannot safely prescribe a repair.
func inspect(run func(context.Context, System, Options) Result, fallback Remediation) func(context.Context, System, Options) Result {
	return func(ctx context.Context, sys System, opts Options) Result {
		r := run(ctx, sys, opts)
		if len(r.Remediation) == 0 && (r.Status == Fail || r.Status == Warn) {
			r.Remediation = []Remediation{fallback}
		}
		return r
	}
}

// AcceptedWarning shares the allowed/accepted marker policy with next.
func AcceptedWarning(r Result) bool {
	marker, _, _ := strings.Cut(r.Fix, ":")
	marker = strings.ToLower(strings.TrimSpace(marker))
	return r.Status == Warn && (marker == "allowed" || marker == "accepted")
}

// RuntimeCheck identifies observations needed before live cluster health.
func RuntimeCheck(name string) bool {
	return name == "lab cluster" || name == "image cache" || name == "lab registry" || name == "moto"
}

// RestartNeeded identifies a stopped main-runtime component, never a scratch cluster.
func RestartNeeded(r Result) bool {
	return r.Status == Stopped && RuntimeCheck(r.Name)
}

// RunObserved defers API-dependent health while a main-runtime restart is needed.
// Results retain catalog order and the ordinary check statuses.
func RunObserved(ctx context.Context, sys System, opts Options, checks []Check) []Result {
	var runtimeChecks, remaining []Check
	for _, check := range checks {
		if RuntimeCheck(check.Name) {
			runtimeChecks = append(runtimeChecks, check)
		} else {
			remaining = append(remaining, check)
		}
	}
	runtimeOpts := opts
	if runtimeOpts.RequestedStacks == nil {
		runtimeOpts.RequestedStacks = []string{}
	}
	runtimeResults := Run(ctx, sys, runtimeOpts, runtimeChecks)
	opts.DeferClusterHealth = slices.ContainsFunc(runtimeResults, RestartNeeded)
	byName := make(map[string]Result, len(checks))
	for _, r := range append(runtimeResults, Run(ctx, sys, opts, remaining)...) {
		byName[r.Name] = r
	}
	results := make([]Result, len(checks))
	for i, check := range checks {
		results[i] = byName[check.Name]
	}
	return results
}

// ShellArg quotes a single argument for a POSIX shell.
func ShellArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\r\n'\"\\$`;&|()<>*?![]{}~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// FixCommands resolves only typed commands, not legacy Fix hints.
func FixCommands(results []Result, opts Options) []FixCommand {
	commands := make([]FixCommand, 0)
	for _, r := range results {
		if AcceptedWarning(r) {
			continue
		}
		fixes := r.Remediation
		if RestartNeeded(r) {
			restart := repair("lab start", "Restart the configured local lab after confirmation.", "changes-lab", false)
			restart.lab, restart.profile = true, true
			fixes = []Remediation{restart}
		}
		if r.Status != Fail && r.Status != Warn && !RestartNeeded(r) {
			continue
		}
		if len(fixes) == 0 {
			fixes = []Remediation{labInspection("lab doctor --read-only", "Review the check and its prerequisites manually.")}
		}
		for _, fix := range fixes {
			if fix.profile {
				fix.Command += " --profile " + ShellArg(opts.Profile)
			}
			if fix.lab {
				if opts.ConfigPath != "" {
					fix.Command += " --config " + ShellArg(opts.ConfigPath)
				}
				if opts.StatePath != "" {
					fix.Command += " --state " + ShellArg(opts.StatePath)
				}
			}
			if fix.kube && opts.Kubeconfig != "" {
				fix.Command += " --kubeconfig " + ShellArg(opts.Kubeconfig)
			}
			commands = append(commands, FixCommand{Check: r.Name, Status: r.Status, Detail: r.Detail, Remediation: fix})
		}
	}
	return commands
}

// FixCommand associates typed remediation with its check.
type FixCommand struct {
	Check  string `json:"check"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Remediation
}

// WriteFixes renders the print-only report; JSON retains the ordinary report fields.
func WriteFixes(w io.Writer, opts Options, results []Result, asJSON bool) error {
	commands := FixCommands(results, opts)
	if asJSON {
		results = slices.Clone(results)
		for i, r := range results {
			if RestartNeeded(r) {
				results[i].Fix = FixCommands([]Result{r}, opts)[0].Command
			}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Report
			Remediations []FixCommand `json:"remediations"`
		}{Report: Report{Profile: opts.Profile, OK: !Failed(results), Results: results}, Remediations: commands})
	}
	var b strings.Builder
	b.WriteString("Fix commands (PRINT ONLY; nothing below was executed):\nRegistry write permission untested (read-only probe).\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "%s %s: %s\n", strings.ToUpper(c.Status.String()), c.Check, c.Detail)
		var labels []string
		if c.RequiresSudo {
			labels = append(labels, "sudo required")
		}
		labels = append(labels, c.Effect)
		if c.RequiresApproval {
			labels = append(labels, "approval required")
		}
		fmt.Fprintf(&b, "  %s\n", strings.Join(labels, "; "))
		prefix := ""
		if c.Manual {
			prefix = "manual action required: "
		}
		fmt.Fprintf(&b, "  %s%s\n", prefix, c.Explanation)
		if c.Manual {
			b.WriteString("  read-only inspection:\n")
		}
		fmt.Fprintf(&b, "  %s\n", c.Command)
	}
	for _, r := range results {
		if r.Status == Skip && r.Detail != "" {
			fmt.Fprintf(&b, "SKIP %s: %s\n", r.Name, r.Detail)
		}
		if r.Status == Stopped && !RestartNeeded(r) {
			fmt.Fprintf(&b, "STOPPED %s: %s\n  guidance: %s\n", r.Name, r.Detail, r.Fix)
		}
	}
	if len(commands) == 0 {
		b.WriteString("No fix commands needed\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
