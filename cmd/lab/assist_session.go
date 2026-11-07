package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/chanupen/devops-lab/internal/assist"
	"github.com/chanupen/devops-lab/internal/kube"
	"github.com/chanupen/devops-lab/internal/tui"
)

func newAssistTools(ctx context.Context, operator *rest.Config) (*assist.Client, error) {
	identity, err := assist.MintIdentity(ctx, operator, assist.ToolServiceAccount)
	if err != nil {
		return nil, err
	}
	return assist.NewClient(identity)
}

func newAssistRunner(ctx context.Context, kubeconfig, caFile string, model assist.HTTPModel, rulesFirst bool, progress func(assist.Progress)) (assist.Runner, error) {
	operator, err := kube.RESTConfig(kubeconfig)
	if err != nil {
		return assist.Runner{}, err
	}
	tools, err := newAssistTools(ctx, operator)
	if err != nil {
		return assist.Runner{}, err
	}
	traceConfig, err := assist.MintIdentity(ctx, operator, assist.TraceServiceAccount)
	if err != nil {
		return assist.Runner{}, err
	}
	client, err := kubernetes.NewForConfig(traceConfig)
	if err != nil {
		return assist.Runner{}, err
	}
	keys, err := client.CoreV1().Secrets("ai").Get(ctx, "langfuse-credentials", metav1.GetOptions{})
	if err != nil {
		return assist.Runner{}, fmt.Errorf("read Langfuse keys: %w", err)
	}
	tracer := assist.OTLPTracer{URL: "https://langfuse.localtest.me:8443",
		PublicKey: string(keys.Data["public-key"]), SecretKey: string(keys.Data["secret-key"])}
	if caFile != "" {
		tracer.Client, err = langfuseClient(caFile)
		if err != nil {
			return assist.Runner{}, err
		}
	}
	return assist.Runner{Tools: tools, Model: model, Tracer: tracer, RulesFirst: rulesFirst, Progress: progress,
		Sensitive: []string{tracer.PublicKey, tracer.SecretKey, model.Key}}, nil
}

type tuiAssistant struct {
	flags      labFlags
	kubeconfig string
	caFile     string
}

func (a tuiAssistant) Alerts(ctx context.Context) ([]assist.Alert, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	operator, err := kube.RESTConfig(a.kubeconfig)
	if err != nil {
		return nil, err
	}
	tools, err := newAssistTools(ctx, operator)
	if err != nil {
		return nil, err
	}
	alerts, err := firingAlerts(ctx, tools, "")
	if len(alerts) > 100 {
		return nil, errors.New("more than 100 firing alerts; use lab assist alert with --namespace/--label")
	}
	return alerts, err
}

func (a tuiAssistant) Run(ctx context.Context, req tui.AssistRequest, progress func(assist.Progress)) (assist.Note, error) {
	ctx, cancel := context.WithTimeout(ctx, assist.RunTimeout)
	defer cancel()
	setup, setupCancel := context.WithTimeout(ctx, 30*time.Second)
	defer setupCancel()
	progress(assist.Progress{Stage: "connecting"})
	name, labels := req.Name, req.Labels
	switch req.Kind {
	case "drill":
		m, err := drillsManager(a.flags)
		if err != nil {
			return assist.Note{}, err
		}
		run, drill, ok, err := m.Active()
		if err != nil {
			return assist.Note{}, err
		}
		if !ok || run.Drill != req.Name {
			return assist.Note{}, errors.New("the named drill is not running")
		}
		name, labels = drill.Alert.Name, drill.Alert.Labels
	case "alert":
	default:
		return assist.Note{}, errors.New("assistant requires an alert or running drill")
	}
	runner, err := newAssistRunner(setup, a.kubeconfig, a.caFile,
		assist.HTTPModel{URL: "http://127.0.0.1:11434", Name: "llama3.2:3b"}, true, progress)
	if err != nil {
		return assist.Note{}, err
	}
	alert, err := findAlert(setup, runner.Tools, name, "", labels)
	if err != nil {
		return assist.Note{}, err
	}
	return runner.Run(ctx, alert)
}
