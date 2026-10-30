package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/state"
)

func newPlanCmd() *cobra.Command {
	var (
		cfgPath   string
		statePath string
		destroy   bool
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show what lab up (or lab down with --destroy) would change",
		Long: "Compares lab.yaml with ~/.lab/state.json and prints the stacks to apply or destroy,\n" +
			"in dependency order. Changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if statePath == "" {
				if statePath, err = state.DefaultPath(); err != nil {
					return err
				}
			}
			g, err := engine.NewGraph(engine.Catalog())
			if err != nil {
				return err
			}
			eng := engine.New(g, state.NewStore(statePath), nil)
			var p engine.Plan
			if destroy {
				p, err = eng.PlanDown(cfg)
			} else {
				p, err = eng.PlanUp(cfg)
			}
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(p)
			}
			return writePlan(out, lipgloss.NewRenderer(out), p)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "lab.yaml", "path to lab.yaml")
	cmd.Flags().StringVar(&statePath, "state", "", "path to the state file (default ~/.lab/state.json)")
	cmd.Flags().BoolVar(&destroy, "destroy", false, "plan lab down instead of lab up")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the plan as JSON")
	return cmd
}

var planMark = map[engine.Action]struct {
	symbol string
	colour lipgloss.Color
}{
	engine.Apply:   {"+", "2"},
	engine.Destroy: {"-", "1"},
	engine.Keep:    {"=", "8"},
}

func writePlan(w io.Writer, r *lipgloss.Renderer, p engine.Plan) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s mode, %s profile, Kubernetes %s\n\n", p.Mode, p.Profile, p.K8sVersion)
	width := 0
	for _, s := range p.Steps {
		width = max(width, len(s.Stack))
	}
	counts := map[engine.Action]int{}
	for _, s := range p.Steps {
		counts[s.Action]++
		m := planMark[s.Action]
		fmt.Fprintf(&b, "  %s %-*s  %s\n", r.NewStyle().Foreground(m.colour).Render(m.symbol), width, s.Stack, s.Reason)
	}
	if len(p.Steps) == 0 {
		b.WriteString("  nothing deployed\n")
	}
	fmt.Fprintf(&b, "\n%d to apply, %d to destroy, %d unchanged\n", counts[engine.Apply], counts[engine.Destroy], counts[engine.Keep])
	_, err := io.WriteString(w, b.String())
	return err
}
