package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/assist"
	"github.com/chanupen/devops-lab/internal/drills"
)

func newAssistCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "assist", Short: "Gather read-only evidence and draft an incident note"}
	cmd.AddCommand(newAssistInputCmd("alert"), newAssistInputCmd("drill"), newAssistEvalCmd())
	return cmd
}

func newAssistEvalCmd() *cobra.Command {
	var f labFlags
	var fixtureDir, models, live string
	var runs int
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Measure local incident-note models against recorded drill evidence",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if live != "" {
				return runAssistLiveEval(cmd, f, live)
			}
			cfgPath, err := f.configPath()
			if err != nil {
				return err
			}
			if cfgPath == "" {
				return errors.New("no lab.yaml in this directory or ~/.lab; pass --config")
			}
			repo, err := repoRoot(cfgPath)
			if err != nil {
				return err
			}
			if fixtureDir == "" {
				fixtureDir = filepath.Join(repo, "internal", "assist", "testdata", "eval")
			}
			fixtures, err := assist.LoadEvalFixtures(fixtureDir)
			if err != nil {
				return fmt.Errorf("load eval fixtures: %w", err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), time.Duration(len(fixtures)*runs*2)*assist.RunTimeout)
			defer cancel()
			var summaries []assist.EvalSummary
			for _, name := range strings.Split(models, ",") {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				for _, hybrid := range []bool{false, true} {
					results, summary, err := assist.Evaluate(ctx, fixtures, assist.HTTPModel{
						URL: "http://127.0.0.1:11434", Name: name,
					}, name, hybrid, runs, func(result assist.EvalResult) error {
						return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
					})
					if err != nil {
						return fmt.Errorf("evaluate %s (hybrid=%t): %w", name, hybrid, err)
					}
					if err := writeAssistEvalCases(cmd.OutOrStdout(), results); err != nil {
						return err
					}
					summaries = append(summaries, summary...)
				}
			}
			return writeAssistEval(cmd.OutOrStdout(), summaries)
		},
	}
	f.register(cmd)
	withLabLock(cmd)
	cmd.Flags().StringVar(&fixtureDir, "fixtures", "", "directory of recorded evaluation fixtures")
	cmd.Flags().StringVar(&models, "models", "llama3.2:3b,qwen2.5:3b", "comma-separated local Ollama models")
	cmd.Flags().IntVar(&runs, "runs", 3, "runs per case and mode (minimum 3)")
	cmd.Flags().StringVar(&live, "live", "", "run one named drill through its safe start/abort lifecycle")
	return cmd
}

func runAssistLiveEval(cmd *cobra.Command, f labFlags, drill string) (runErr error) {
	m, err := drillsManager(f)
	if err != nil {
		return err
	}
	if _, err := m.Start(cmd.Context(), drill, false, cmd.ErrOrStderr()); err != nil {
		return err
	}
	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), drills.ScriptTimeout)
		defer cancel()
		if _, err := m.Abort(restoreCtx, cmd.ErrOrStderr()); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("restore live drill: %w", err))
		}
	}()
	alertCtx, cancel := context.WithTimeout(cmd.Context(), 7*time.Minute)
	defer cancel()
	for {
		run, err := m.CheckAlert(alertCtx)
		if err != nil {
			return err
		}
		if !run.AlertFired.IsZero() {
			break
		}
		select {
		case <-alertCtx.Done():
			return fmt.Errorf("wait for drill alert: %w", alertCtx.Err())
		case <-time.After(5 * time.Second):
		}
	}
	input := newAssistInputCmd("drill")
	input.SetOut(cmd.OutOrStdout())
	input.SetErr(cmd.ErrOrStderr())
	args := []string{drill, "--rules-first"}
	if cfgPath, err := f.configPath(); err == nil && cfgPath != "" {
		args = append(args, "--config", cfgPath)
	}
	if f.statePath != "" {
		args = append(args, "--state", f.statePath)
	}
	input.SetArgs(args)
	return input.ExecuteContext(cmd.Context())
}

func writeAssistEval(w io.Writer, summaries []assist.EvalSummary) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "MODEL\tMODE\tCAUSE CORRECT\tABSTENTIONS\tEVIDENCE SUPPORT\tMEAN TIME\tTIME STDDEV\tRUNS")
	for _, summary := range summaries {
		mode := "model-only"
		if summary.Hybrid {
			mode = "rules-first"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%.1f%%\t%d/%d\t%.1f%%\t%s\t%s\t%d\n",
			summary.Model, mode, summary.CauseCorrect*100,
			summary.AbstainCorrect, summary.AbstainExpected, summary.EvidenceSupport*100,
			summary.MeanDuration.Round(time.Millisecond), summary.DurationVariance.Round(time.Millisecond), summary.Runs)
	}
	return tw.Flush()
}

func writeAssistEvalCases(w io.Writer, results []assist.EvalResult) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "\nCASE\tMODEL\tMODE\tRUNS\tTOTAL WALL TIME\tCAUSE CORRECT\tABSTENTION\tEVIDENCE SUPPORT")
	type row struct {
		caseID, model, mode string
		runs                int
		wall                time.Duration
		correct, support    float64
		causeScored         int
		abstainExpected     bool
		abstainCorrect      int
	}
	rows := make([]row, 0, len(results))
	index := map[string]int{}
	for _, result := range results {
		mode := "model-only"
		if result.Hybrid {
			mode = "rules-first"
		}
		key := result.Case + "\x00" + result.Model + "\x00" + mode
		i, ok := index[key]
		if !ok {
			i = len(rows)
			index[key] = i
			rows = append(rows, row{caseID: result.Case, model: result.Model, mode: mode})
		}
		rows[i].runs++
		rows[i].wall += result.Duration
		if result.CauseScored {
			rows[i].causeScored++
		}
		if result.CauseCorrect {
			rows[i].correct++
		}
		rows[i].support += result.EvidenceSupport
		rows[i].abstainExpected = result.AbstainExpected
		if result.AbstainCorrect {
			rows[i].abstainCorrect++
		}
	}
	for _, row := range rows {
		abstention := "—"
		if row.abstainExpected {
			abstention = fmt.Sprintf("%d/%d", row.abstainCorrect, row.runs)
		}
		cause := "not scored"
		if row.causeScored > 0 {
			cause = fmt.Sprintf("%.1f%%", row.correct/float64(row.causeScored)*100)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%.1f%%\n",
			row.caseID, row.model, row.mode, row.runs, row.wall.Round(time.Millisecond),
			cause, abstention, row.support/float64(row.runs)*100)
	}
	return tw.Flush()
}

func newAssistInputCmd(kind string) *cobra.Command {
	var f labFlags
	var hosted bool
	var rulesFirst bool
	var hostedURL, hostedModel, caFile, namespace string
	var selectors []string
	cmd := &cobra.Command{
		Use:   kind + " <name>",
		Short: "Investigate a local " + kind,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, store, _, err := f.load()
			if err != nil {
				return err
			}
			if cfg.Mode != "local" {
				return errors.New("assist is available only in local mode")
			}
			kubeconfig := filepath.Join(filepath.Dir(store.Path()), "kubeconfig")
			ctx, cancel := context.WithTimeout(cmd.Context(), assist.RunTimeout)
			defer cancel()
			setupCtx, setupCancel := context.WithTimeout(ctx, 30*time.Second)
			defer setupCancel()
			model := assist.HTTPModel{URL: "http://127.0.0.1:11434", Name: "llama3.2:3b"}
			if hosted {
				if hostedURL == "" || hostedModel == "" || os.Getenv("LAB_HOSTED_API_KEY") == "" {
					return errors.New("--hosted requires --hosted-url, --hosted-model and LAB_HOSTED_API_KEY")
				}
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Hosted inference enabled: selected cluster evidence will leave this host.")
				model = assist.HTTPModel{URL: hostedURL, Name: hostedModel, Key: os.Getenv("LAB_HOSTED_API_KEY"), Hosted: true}
			} else if hostedURL != "" || hostedModel != "" {
				return errors.New("hosted options require --hosted")
			}
			alertName := args[0]
			labels, err := alertSelectors(selectors)
			if err != nil {
				return err
			}
			if kind == "drill" {
				m, err := drillsManager(f)
				if err != nil {
					return err
				}
				run, drill, ok, err := m.Active()
				if err != nil {
					return err
				}
				if !ok || run.Drill != args[0] {
					return errors.New("the named drill is not running")
				}
				alertName = drill.Alert.Name
				for k, v := range drill.Alert.Labels {
					if selected, ok := labels[k]; ok && selected != v {
						return fmt.Errorf("label %s conflicts with the drill's alert", k)
					}
					labels[k] = v
				}
			}
			runner, err := newAssistRunner(setupCtx, kubeconfig, caFile, model, rulesFirst, nil)
			if err != nil {
				return err
			}
			alert, err := findAlert(setupCtx, runner.Tools, alertName, namespace, labels)
			if err != nil {
				return err
			}
			note, runErr := runner.Run(ctx, alert)
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if err := enc.Encode(note); err != nil {
				return err
			}
			return runErr
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVar(&hosted, "hosted", false, "send selected evidence to a hosted model")
	cmd.Flags().BoolVar(&rulesFirst, "rules-first", false, "retain a deterministic cause supported by the evidence")
	cmd.Flags().StringVar(&hostedURL, "hosted-url", "", "hosted OpenAI-compatible HTTPS API base URL")
	cmd.Flags().StringVar(&hostedModel, "hosted-model", "", "hosted model name")
	cmd.Flags().StringVar(&caFile, "langfuse-ca", "", "PEM CA certificate for the local Langfuse Gateway")
	cmd.Flags().StringVar(&namespace, "namespace", "", "select an alert in this namespace")
	cmd.Flags().StringArrayVar(&selectors, "label", nil, "select an exact firing-alert label (key=value; repeatable)")
	return cmd
}

func findAlert(ctx context.Context, tools assist.Tools, name, namespace string, labels map[string]string) (assist.Alert, error) {
	alerts, err := firingAlerts(ctx, tools, name)
	if err != nil {
		return assist.Alert{}, err
	}
	var matches []assist.Alert
	for _, alert := range alerts {
		if namespace != "" && alert.Labels["namespace"] != namespace {
			continue
		}
		match := true
		for k, v := range labels {
			if alert.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			matches = append(matches, alert)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return assist.Alert{}, fmt.Errorf("no firing alert %q", name)
	}
	return assist.Alert{}, fmt.Errorf("multiple firing alerts %q; select a namespace or --label", name)
}

func alertSelectors(selectors []string) (map[string]string, error) {
	labels := make(map[string]string, len(selectors))
	for _, selector := range selectors {
		k, v, ok := strings.Cut(selector, "=")
		if !ok || k == "" {
			return nil, errors.New("--label requires key=value")
		}
		if previous, ok := labels[k]; ok && previous != v {
			return nil, fmt.Errorf("conflicting --label values for %s", k)
		}
		labels[k] = v
	}
	return labels, nil
}

func firingAlerts(ctx context.Context, tools assist.Tools, name string) ([]assist.Alert, error) {
	query := `ALERTS{alertstate="firing"}`
	if name != "" {
		query = `ALERTS{alertstate="firing",alertname=` + strconv.Quote(name) + `}`
	}
	raw, _ := json.Marshal(map[string]string{"query": query})
	b, err := tools.Call(ctx, "prometheus_query", raw)
	if err != nil {
		return nil, err
	}
	var result struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &result); err != nil || result.Status != "success" {
		return nil, errors.New("cannot decode firing alerts")
	}
	var alerts []assist.Alert
	for _, entry := range result.Data.Result {
		alertName := name
		if alertName == "" {
			alertName = entry.Metric["alertname"]
		}
		if alertName == "" {
			continue
		}
		alerts = append(alerts, assist.Alert{Name: alertName, Status: "firing", Labels: entry.Metric})
	}
	return alerts, nil
}

func langfuseClient(path string) (*http.Client, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Langfuse CA: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("langfuse CA contains no certificates")
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirects are not allowed") }}, nil
}
