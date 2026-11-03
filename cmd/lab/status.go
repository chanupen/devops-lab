package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/gitops"
	"github.com/chanupen/devops-lab/internal/kube"
	labaws "github.com/chanupen/devops-lab/internal/provider/aws"
	"github.com/chanupen/devops-lab/internal/provider/local"
	"github.com/chanupen/devops-lab/internal/state"
)

func newStatusCmd(newProvider providerFactory) *cobra.Command {
	var flags labFlags
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show stacks, the GitOps revision and app URLs",
		Long: "Reads ~/.lab/state.json and lists every stack in dependency order and the Git revision\n" +
			"the gitops stack syncs. Once the cluster is up it adds live node and Argo CD app health,\n" +
			"and once core is up the app URLs. Changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, store, g, err := flags.load()
			if err != nil {
				return err
			}
			st, err := store.Load()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			r := lipgloss.NewRenderer(out)
			if err := writeStatus(out, r, cfg, st, g); err != nil {
				return err
			}
			if st.IsApplied(clusterStack) {
				h, err := readLive(cmd.Context(), filepath.Join(homeOf(store), "kubeconfig"))
				var stopped string
				if err != nil && cfg.Mode == "local" {
					stopped = stoppedFix(cmd.Context())
				}
				if err := writeLive(out, r, h, err, stopped); err != nil {
					return err
				}
			}
			if cfg.Mode == "cloud" {
				if exp, err := labaws.ReadExpiry(labaws.ExpiryFile(homeOf(store))); err == nil {
					_, _ = fmt.Fprintf(out, "\nexpires-at %s (in %s); lab extend 1h pushes it forward\n",
						exp.Local().Format("2006-01-02 15:04 MST"), time.Until(exp).Round(time.Minute))
				}
			}
			if !st.IsApplied(gatewayStack) {
				return nil
			}
			prov, err := newProvider(cfg, homeOf(store), runOptions{Revision: st.Revision})
			if err != nil {
				_, err = fmt.Fprintf(out, "\nwarning: could not list app URLs: %v\n", err)
				return err
			}
			return printApps(cmd, prov, store)
		},
	}
	flags.register(cmd)
	flags.registerMode(cmd)
	return cmd
}

func writeStatus(w io.Writer, r *lipgloss.Renderer, cfg config.Config, st state.State, g *engine.Graph) error {
	var b strings.Builder
	mode := st.Mode
	if mode == "" {
		mode = cfg.Mode
	}
	fmt.Fprintf(&b, "%s mode, %s profile, Kubernetes %s\n", mode, engine.ProfileName(cfg), cfg.ClusterK8sVersion())
	warn := r.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	switch {
	case !st.IsApplied(engine.RevisionStack):
		b.WriteString("gitops revision: - (gitops not deployed)\n")
	case st.Revision == engine.DefaultRevision:
		fmt.Fprintf(&b, "gitops revision: %s\n", st.Revision)
	case st.Revision == "":
		b.WriteString("gitops revision: " + warn.Render("unrecorded") + " (lab up records it and moves gitops to main)\n")
	default:
		fmt.Fprintf(&b, "gitops revision: %s (not %s; a plain lab up moves it back)\n",
			warn.Render(st.Revision), engine.DefaultRevision)
	}
	b.WriteString("\n")
	all, err := g.Order(g.Names()...)
	if err != nil {
		return err
	}
	width := 0
	for _, s := range all {
		width = max(width, len(s))
	}
	for _, s := range all {
		ss, ok := st.Stacks[s]
		sym, col, text := "○", lipgloss.Color("8"), "not deployed"
		switch {
		case ok && ss.Status == state.Applied:
			sym, col, text = "●", lipgloss.Color("2"), "applied"
		case ok && ss.Status == state.Failed:
			sym, col, text = "✕", lipgloss.Color("1"), "failed: "+ss.Error
		}
		when := ""
		if ok && !ss.UpdatedAt.IsZero() {
			when = "  " + r.NewStyle().Faint(true).Render(ss.UpdatedAt.Local().Format("2006-01-02 15:04"))
		}
		fmt.Fprintf(&b, "  %s %-*s  %s%s\n", r.NewStyle().Foreground(col).Render(sym), width, s, text, when)
	}
	_, err = io.WriteString(w, b.String())
	return err
}

// clusterStack is the stack that makes the cluster lab status reads.
const clusterStack = "cluster"

// liveTimeout bounds lab status's look at the cluster.
const liveTimeout = 5 * time.Second

// readLive lists the cluster's nodes and Argo CD apps once.
func readLive(ctx context.Context, kubeconfig string) (kube.Health, error) {
	if _, err := os.Stat(kubeconfig); err != nil {
		return kube.Health{}, fmt.Errorf("no kubeconfig at %s", kubeconfig)
	}
	cfg, err := kube.RESTConfig(kubeconfig)
	if err != nil {
		return kube.Health{}, err
	}
	cfg.Timeout = liveTimeout
	c, err := kube.NewClients(kube.WarningsTo(cfg, io.Discard))
	if err != nil {
		return kube.Health{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, liveTimeout)
	defer cancel()
	return kube.ReadHealth(ctx, c, gitops.Namespace)
}

// stoppedFix is the command that starts the main cluster's nodes when they
// are there but stopped (after a reboot); "" otherwise.
func stoppedFix(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, liveTimeout)
	defer cancel()
	units, err := local.HostUnits(ctx)
	if err != nil {
		return ""
	}
	for _, u := range units {
		if u.Kind == "cluster" && u.Name == local.ClusterName && !u.Running() {
			return u.Fix()
		}
	}
	return ""
}

// writeLive prints what the cluster says now: nodes, then the Argo CD apps,
// each one that is not Synced and Healthy on its own line.
func writeLive(w io.Writer, r *lipgloss.Renderer, h kube.Health, err error, stoppedFix string) error {
	sym := func(s, col string) string { return r.NewStyle().Foreground(lipgloss.Color(col)).Render(s) }
	var b strings.Builder
	b.WriteString("\nlive:\n")
	switch {
	case err != nil && stoppedFix != "":
		fmt.Fprintf(&b, "  cluster  %s stopped; lab up starts it (or %s)\n", sym("○", "8"), stoppedFix)
	case err != nil:
		fmt.Fprintf(&b, "  cluster  %s off (%s)\n", sym("○", "8"), firstLine(err.Error()))
	default:
		s := sym("●", "2")
		if h.Ready < h.Nodes || h.Nodes == 0 {
			s = sym("◐", "3")
		}
		fmt.Fprintf(&b, "  nodes    %s %d/%d Ready\n", s, h.Ready, h.Nodes)
		writeLiveApps(&b, sym, h.Apps)
	}
	_, err = io.WriteString(w, b.String())
	return err
}

func writeLiveApps(b *strings.Builder, sym func(s, col string) string, apps []kube.App) {
	if apps == nil {
		fmt.Fprintf(b, "  apps     %s Argo CD is not installed\n", sym("○", "8"))
		return
	}
	ready := 0
	var rest []string
	for _, a := range apps {
		switch {
		case a.Ready():
			ready++
			continue
		case a.Health == "Degraded" || len(a.Problems) > 0:
			line := fmt.Sprintf("%s %s  %s  %s", sym("✕", "1"), a.Name, orDash(a.Sync), orDash(a.Health))
			if len(a.Problems) > 0 {
				line += "  (" + firstLine(a.Problems[0]) + ")"
			}
			rest = append(rest, line)
		default:
			rest = append(rest, fmt.Sprintf("%s %s  %s  %s", sym("◐", "3"), a.Name, orDash(a.Sync), orDash(a.Health)))
		}
	}
	s := sym("●", "2")
	if len(rest) > 0 {
		s = sym("◐", "3")
	}
	fmt.Fprintf(b, "  apps     %s %d/%d Synced and Healthy\n", s, ready, len(apps))
	for _, l := range rest {
		fmt.Fprintf(b, "           %s\n", l)
	}
}

// firstLine is s up to its first newline.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
