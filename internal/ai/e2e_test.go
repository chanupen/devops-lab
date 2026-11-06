//go:build e2e

package ai

// The ai stack end to end on the main lab, inside the ai namespace only:
// the NetworkPolicy keeps unlabelled pods away from Ollama, then the smoke
// Job asks llama3.2:3b one question through ollama.ai.svc (three runs: the
// first may load the model into VRAM) and each trace is found in Langfuse.
//
// Needs a lab with the ai stack (lab up --stacks ai) and its kubeconfig:
//
//	LAB_E2E_MAIN_KUBECONFIG=~/.lab/kubeconfig go test -tags e2e -run E2EAI -v -timeout 20m ./internal/ai/

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/chanupen/devops-lab/internal/kube"
)

const (
	model = "llama3.2:3b"
	// curlImage is curl 8.17.0 by digest, as lab 3 and sample-db use it.
	curlImage = "docker.io/curlimages/curl:8.17.0@sha256:935d9100e9ba842cdb060de42472c7ca90cfe9a7c96e4dacb55e79e560b3ff40"
)

func TestE2EAI(t *testing.T) {
	path := os.Getenv("LAB_E2E_MAIN_KUBECONFIG")
	if path == "" {
		t.Skip("LAB_E2E_MAIN_KUBECONFIG not set")
	}
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = home + path[1:]
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		t.Fatal(err)
	}
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()

	t.Run("network policy", func(t *testing.T) {
		tests := []struct {
			name     string
			labelled bool
			url      string
			reach    bool
		}{
			{"unlabelled pod cannot reach Ollama", false, "http://ollama.ai.svc:11434/api/version", false},
			{"unlabelled pod reaches Langfuse", false, "http://langfuse-web.ai.svc:3000/api/public/health", true},
			{"labelled pod reaches Ollama", true, "http://ollama.ai.svc:11434/api/version", true},
		}
		for i, tt := range tests {
			got, out := probe(ctx, t, core, fmt.Sprintf("lab-ai-probe-%d-%d", time.Now().Unix(), i), tt.labelled, tt.url)
			if got != tt.reach {
				t.Errorf("%s: reached = %v (%s)", tt.name, got, out)
			}
		}
	})

	var traces []string
	for i := 1; i <= 3; i++ {
		r, err := RunSmoke(ctx, core, model, 5*time.Minute, testWriter{t})
		if err != nil {
			t.Fatalf("smoke run %d: %v", i, err)
		}
		t.Logf("run %d: latency %.2fs (model load %.2fs), prompt %d tokens at %.1f tok/s, answer %d tokens at %.1f tok/s",
			i, r.LatencySeconds, r.LoadSeconds, r.PromptTokens, r.PromptTokensPerSecond, r.OutputTokens, r.OutputTokensPerSecond)
		t.Logf("run %d: answer: %s", i, r.Answer)
		traces = append(traces, r.TraceID)
	}

	pod, ports, err := kube.PodTarget(ctx, core, Namespace, "langfuse-web", []kube.Forward{{Local: 13300, ServicePort: 3000}})
	if err != nil {
		t.Fatal(err)
	}
	fctx, stop := context.WithCancel(ctx)
	defer stop()
	ready := make(chan struct{})
	go func() { _ = kube.PortForward(fctx, cfg, Namespace, pod, ports, ready, io.Discard) }()
	select {
	case <-ready:
	case <-time.After(30 * time.Second):
		t.Fatal("port-forward to langfuse-web not ready after 30s")
	}
	for _, id := range traces {
		var tr Trace
		deadline := time.Now().Add(2 * time.Minute)
		for {
			tr, err = FindTrace(ctx, core, "http://127.0.0.1:13300", id)
			if err == nil && len(tr.Observations) > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("trace %s not in Langfuse after 2m (last: %v, %d observations)", id, err, len(tr.Observations))
			}
			time.Sleep(3 * time.Second)
		}
		o := tr.Observations[0]
		if o.TraceID != id || o.Name != "smoke-question" || !slices.Contains([]string{"GENERATION", "generation"}, o.Type) {
			t.Errorf("trace %s: observation %+v", id, o)
		}
		t.Logf("trace %s in Langfuse: %s %s", id, o.Type, o.Name)
	}
}

// probe runs curl in a Job in the ai namespace and reports whether it got
// an HTTP 200 from url.
func probe(ctx context.Context, t *testing.T, core kubernetes.Interface, name string, labelled bool, url string) (bool, string) {
	t.Helper()
	j := SmokeJob(name, model)
	pod := &j.Spec.Template
	if !labelled {
		delete(j.Labels, OllamaLabel)
		delete(pod.Labels, OllamaLabel)
	}
	c := &pod.Spec.Containers[0]
	c.Image, c.Env = curlImage, nil
	c.Command = []string{"curl", "-sf", "-m", "5", "-o", "/dev/null", "-w", "%{http_code}", url}
	jobs := core.BatchV1().Jobs(Namespace)
	if _, err := jobs.Create(ctx, j, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		bg := metav1.DeletePropagationBackground
		_ = jobs.Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{PropagationPolicy: &bg})
	}()
	var job *batchv1.Job
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		got, err := jobs.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Status.Succeeded > 0 || got.Status.Failed > 0 {
			job = got
			break
		}
	}
	if job == nil {
		t.Fatalf("probe %s did not finish in 2m", name)
	}
	out, _ := jobLog(ctx, core, name)
	return job.Status.Succeeded > 0 && strings.TrimSpace(out) == fmt.Sprint(http.StatusOK), out
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
