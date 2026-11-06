package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/chanupen/devops-lab/internal/assist"
	"github.com/chanupen/devops-lab/internal/kube"
)

func newAssistCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "assist", Short: "Gather read-only evidence and draft an incident note"}
	cmd.AddCommand(newAssistInputCmd("alert"), newAssistInputCmd("drill"))
	return cmd
}

func newAssistInputCmd(kind string) *cobra.Command {
	var f labFlags
	var hosted bool
	var hostedURL, hostedModel, caFile, namespace string
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
			operator, err := kube.RESTConfig(kubeconfig)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), assist.RunTimeout)
			defer cancel()
			setupCtx, setupCancel := context.WithTimeout(ctx, 30*time.Second)
			defer setupCancel()
			toolConfig, err := assist.MintIdentity(setupCtx, operator, assist.ToolServiceAccount)
			if err != nil {
				return err
			}
			tools, err := assist.NewClient(toolConfig)
			if err != nil {
				return err
			}
			traceConfig, err := assist.MintIdentity(setupCtx, operator, assist.TraceServiceAccount)
			if err != nil {
				return err
			}
			traceClient, err := kubernetes.NewForConfig(traceConfig)
			if err != nil {
				return err
			}
			keys, err := traceClient.CoreV1().Secrets("ai").Get(setupCtx, "langfuse-credentials", metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("read Langfuse keys: %w", err)
			}
			tracer := assist.OTLPTracer{
				URL: "https://langfuse.localtest.me:8443", PublicKey: string(keys.Data["public-key"]), SecretKey: string(keys.Data["secret-key"]),
			}
			if caFile != "" {
				tracer.Client, err = langfuseClient(caFile)
				if err != nil {
					return err
				}
			}
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
			var labels map[string]string
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
				alertName, labels = drill.Alert.Name, drill.Alert.Labels
			}
			alert, err := findAlert(setupCtx, tools, alertName, namespace, labels)
			if err != nil {
				return err
			}
			note, runErr := (assist.Runner{Tools: tools, Model: model, Tracer: tracer,
				Sensitive: []string{tracer.PublicKey, tracer.SecretKey, model.Key}}).Run(ctx, alert)
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
	cmd.Flags().StringVar(&hostedURL, "hosted-url", "", "hosted OpenAI-compatible HTTPS API base URL")
	cmd.Flags().StringVar(&hostedModel, "hosted-model", "", "hosted model name")
	cmd.Flags().StringVar(&caFile, "langfuse-ca", "", "PEM CA certificate for the local Langfuse Gateway")
	cmd.Flags().StringVar(&namespace, "namespace", "", "select an alert in this namespace")
	return cmd
}

func findAlert(ctx context.Context, tools assist.Tools, name, namespace string, labels map[string]string) (assist.Alert, error) {
	raw, _ := json.Marshal(map[string]string{"query": `ALERTS{alertstate="firing",alertname=` + strconv.Quote(name) + `}`})
	b, err := tools.Call(ctx, "prometheus_query", raw)
	if err != nil {
		return assist.Alert{}, err
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
		return assist.Alert{}, errors.New("cannot decode firing alerts")
	}
	var matches []map[string]string
	for _, entry := range result.Data.Result {
		if namespace != "" && entry.Metric["namespace"] != namespace {
			continue
		}
		match := true
		for k, v := range labels {
			if entry.Metric[k] != v {
				match = false
				break
			}
		}
		if match {
			matches = append(matches, entry.Metric)
		}
	}
	if len(matches) == 1 {
		return assist.Alert{Name: name, Status: "firing", Labels: matches[0]}, nil
	}
	if len(matches) == 0 {
		return assist.Alert{}, fmt.Errorf("no firing alert %q", name)
	}
	return assist.Alert{}, fmt.Errorf("multiple firing alerts %q; select a namespace", name)
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
