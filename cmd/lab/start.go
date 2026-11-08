package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/doctor"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/state"
)

func newStartCmd(factory providerFactory, deps guidedDeps) *cobra.Command {
	var flags labFlags
	var profile string
	var yes, dry bool
	cmd := &cobra.Command{
		Use: "start", Short: "Check readiness, select a local profile and confirm its plan",
		Long: "Local Linux only. Selects the largest profile that fits available RAM (4/12/19 GiB),\n" +
			"keeps extraStacks, runs read-only doctor preflight and shows the plan before confirmation.\n" +
			"The profile override is temporary. Active labs/drills must be finished explicitly.\n" +
			"Dry-run never applies, restarts containers, downloads charts or writes state/locks.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, err := loadGuided(cmd, flags, deps)
			if err != nil {
				return err
			}
			original := in
			if profile != "" {
				if _, err := doctor.ProfileMemoryBytes(profile); err != nil {
					return err
				}
			}
			sys := deps.system(homeOf(in.store), true)
			mem, err := doctor.ReadMemory(sys)
			if err != nil {
				return fmt.Errorf("memory preflight: %w", err)
			}
			selected, selectionErr := selectStartProfile(mem, profile)
			in.cfg.Profile = selected
			eng := engine.New(in.graph, in.store, nil)
			p, err := eng.PlanUp(in.cfg, engine.PlanOptions{Supports: stackSupport(in.cfg)})
			if err != nil {
				return err
			}
			results := collectGuided(cmd.Context(), in, deps, mem, sys, p)
			out := cmd.OutOrStdout()
			if err := doctor.WriteTable(out, lipgloss.NewRenderer(out), results); err != nil {
				return err
			}
			source := "automatic"
			if profile != "" {
				source = "explicit"
			}
			need, _ := doctor.ProfileMemoryBytes(selected)
			if _, err := fmt.Fprintf(out, "\nProfile: %s (%s); %.1f GiB available; threshold %d GiB\n", selected, source,
				float64(mem.Available)/(1<<30), need/(1<<30)); err != nil {
				return err
			}
			if len(in.cfg.ExtraStacks) > 0 {
				if _, err := fmt.Fprintf(out, "Extra stacks: %s; memory estimates are not a hard capacity guarantee\n", strings.Join(in.cfg.ExtraStacks, ", ")); err != nil {
					return err
				}
			}
			blocked := selectionErr != nil || in.st.ActiveLab != "" || doctor.Failed(results)
			if r, _ := in.activeDrill(); r != nil {
				blocked = true
			}
			if blocked {
				if err := writePlan(out, lipgloss.NewRenderer(out), p); err != nil {
					return err
				}
			}
			if selectionErr != nil {
				return selectionErr
			}
			if r, path := in.activeDrill(); r != nil {
				selectedInput := in
				selectedInput.store = state.NewStore(path)
				return fmt.Errorf("a drill is active; finish it explicitly: %s", guidedCommand("drills abort", selectedInput))
			}
			if in.st.ActiveLab != "" {
				return fmt.Errorf("a lab is active; inspect its cleanup first: %s", guidedCommand("labs status", in))
			}
			if doctor.Failed(results) {
				return fmt.Errorf("doctor preflight failed; no changes made")
			}
			// Provider constructors and these observations are read-only. Reuse
			// staleness discovery so a retry can consent to the actual work.
			observeCtx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			prov, err := factory(in.cfg, homeOf(in.store), runOptions{Timeout: 30 * time.Minute})
			if err == nil {
				if gate, ok := prov.(versionGate); ok {
					err = gate.CheckK8sVersion(observeCtx)
				}
			}
			if err == nil {
				p, err = observeUpPlan(observeCtx, prov, eng, in.cfg, engine.PlanOptions{})
			}
			cancel()
			if err != nil {
				return fmt.Errorf("read-only plan observations failed: %w", err)
			}
			if err := writePlan(out, lipgloss.NewRenderer(out), p); err != nil {
				return err
			}
			for _, r := range results {
				if r.Status == doctor.Stopped && (r.Name == "lab cluster" || r.Name == "image cache") {
					if _, err := fmt.Fprintf(out, "Restart proposed: %s (%s)\n", r.Name, r.Fix); err != nil {
						return err
					}
				}
			}
			if dry {
				_, err := fmt.Fprintln(out, "Dry run: no apply, restart, chart download or state write.")
				return err
			}
			if err := confirmStart(cmd, deps, yes); err != nil {
				return err
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			unlock, err := lockLab(cmd)
			if err != nil {
				return err
			}
			defer unlock()
			fresh, err := loadGuided(cmd, flags, deps)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(original.cfg, fresh.cfg) || !reflect.DeepEqual(original.st, fresh.st) ||
				!reflect.DeepEqual(original.e2e, fresh.e2e) || original.cfgPath != fresh.cfgPath {
				return fmt.Errorf("inputs changed while confirming; no changes made; run lab start again")
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			command := guidedCommand("start", in, "--profile", selected)
			if err := executeUp(cmd, factory, flags, in.cfg, in.store, in.graph, engine.PlanOptions{},
				30*time.Minute, command, true, &p); err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "\nNext steps:\n  %s\n  %s\n  %s\nOpening the TUI can hold the lab mutation lock.\n",
				guidedCommand("status", in), guidedCommand("next", in), guidedCommand("", in))
			return err
		},
	}
	flags.register(cmd)
	cmd.Flags().StringVar(&profile, "profile", "", "temporary profile override: minimal, standard, full (default: available RAM)")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the local plan without prompting; does not bypass readiness")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "show readiness and the plan without applying or writing")
	return cmd
}

func confirmStart(cmd *cobra.Command, deps guidedDeps, yes bool) error {
	if err := cmd.Context().Err(); err != nil {
		return err
	}
	result := make(chan error, 1)
	go func() { result <- deps.confirm(cmd, yes, "Start this local plan?") }()
	select {
	case err := <-result:
		return err
	case <-cmd.Context().Done():
		return cmd.Context().Err()
	}
}
