package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/doctor"
	"github.com/chanupen/devops-lab/internal/drills"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/state"
)

type nextStep struct {
	SchemaVersion int    `json:"schemaVersion"`
	Kind          string `json:"kind"`
	Reason        string `json:"reason"`
	Command       string `json:"command"`
	Effect        string `json:"effect"`
	RequiresSudo  bool   `json:"requiresSudo"`
}

func newNextCmd(deps guidedDeps) *cobra.Command {
	var flags labFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use: "next", Short: "Suggest one local next step without executing or recording it",
		Long: "Reads local state, catalogs and fresh observational doctor results. Prints one exact\n" +
			"command and its effect, never executes it or records observations. A successful\n" +
			"suggestion does not mean the lab is healthy. No cloud probes or lab lock.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, err := loadGuided(cmd, flags, deps)
			if err != nil {
				return err
			}
			ls, ds, err := deps.catalog(in.cfgPath)
			if err != nil {
				return fmt.Errorf("catalogs: %w", err)
			}
			sys := deps.system(homeOf(in.store), true)
			mem, err := doctor.ReadMemory(sys)
			if err != nil {
				return fmt.Errorf("memory preflight: %w", err)
			}
			p, err := engine.New(in.graph, in.store, nil).PlanUp(in.cfg, engine.PlanOptions{Supports: stackSupport(in.cfg)})
			if err != nil {
				return err
			}
			results := collectGuided(cmd.Context(), in, deps, mem, sys, p)
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			step := recommendNext(in, deps, p, results, ls, ds)
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(step)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Next: %s\nCommand: %s\nEffect: %s; not executed.\n", step.Reason, step.Command, step.Effect)
			return err
		},
	}
	flags.register(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "print one recommendation as JSON")
	return cmd
}

func recommendNext(in guidedInput, deps guidedDeps, p engine.Plan, results []doctor.Result, ls []labs.Lab, ds []drills.Drill) nextStep {
	makeStep := func(kind, reason, command, effect string) nextStep {
		return nextStep{SchemaVersion: 1, Kind: kind, Reason: reason, Command: command, Effect: effect}
	}
	if r, path := in.activeDrill(); r != nil {
		selected := in
		selected.store = state.NewStore(path)
		if r.Phase != state.DrillRunning || !deps.now().Before(r.Deadline) {
			return makeStep("restore-drill", "Restore the interrupted or expired drill explicitly.",
				guidedCommand("drills abort", selected), "changes-lab")
		}
		return makeStep("drill", "Inspect the running drill's symptoms and timers.",
			guidedCommand("drills status", selected), "writes-state")
	}
	diagnostic := func(r doctor.Result) nextStep {
		return makeStep("doctor", "Review doctor instructions for "+r.Name+".",
			guidedCommand("doctor", in, "--read-only", "--profile", in.cfg.Profile), "read-only")
	}
	for _, r := range results {
		if r.Status == doctor.Fail {
			return diagnostic(r)
		}
	}
	if in.st.ActiveLab != "" {
		return makeStep("lab", "Inspect the active lab and its cleanup guidance.", guidedCommand("labs status", in), "read-only")
	}
	start := func(reason string) nextStep {
		return makeStep("start", reason, guidedCommand("start", in, "--profile", in.cfg.Profile), "changes-lab")
	}
	for _, r := range results {
		if guidedRestart(r) {
			return start("Restart the configured local lab after confirmation.")
		}
	}
	if len(in.st.Stacks) == 0 || p.Count(engine.Apply) > 0 {
		return start("Bring up the configured local profile.")
	}
	for _, r := range results {
		marker, _, _ := strings.Cut(r.Fix, ":")
		marker = strings.ToLower(strings.TrimSpace(marker))
		if r.Status == doctor.Warn && marker != "allowed" && marker != "accepted" {
			return diagnostic(r)
		}
	}
	sortGuidedLabs(ls)
	for _, l := range ls {
		if eligibleGuidedLab(l, in, deps) {
			return makeStep("start-lab", "Start the next eligible lab.", guidedCommand("labs start", in, l.ID), "changes-lab")
		}
	}
	best := in.st.Progress.DrillBests()
	if slices.ContainsFunc(drills.Eligible(ds, in.st), func(d drills.Drill) bool { return best[d.ID].Recovered == 0 }) {
		return makeStep("start-drill", "Practise an eligible drill without revealing its identity.",
			guidedCommand("drills start", in, "--random"), "changes-lab")
	}
	return makeStep("review", "Review progress and replay options.", guidedCommand("labs list", in), "read-only")
}
