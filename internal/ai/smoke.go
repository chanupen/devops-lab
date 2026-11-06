// Package ai holds the ai stack's checks that run in the cluster: the
// smoke test asks the host's Ollama one question through ollama.ai.svc and
// traces it in Langfuse.
package ai

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
)

// Names and pins of the smoke test.
const (
	Namespace = "ai"
	// SmokeImage is Python 3.13 on Alpine, by digest: the script needs only
	// the standard library.
	SmokeImage = "docker.io/library/python:3.13.18-alpine3.24@sha256:1e1f6334529d1fb26a2ef619d7cffb30a7c7d7cc1cc7338ebee283a58d1aeaca"
	// OllamaLabel lets a pod in the ai namespace reach Ollama (the stack's
	// NetworkPolicy).
	OllamaLabel = "lab.devops/ollama"
	// AppSecret holds Langfuse's project API keys (ESO, from Secrets Manager).
	AppSecret = "langfuse-credentials"
	// Question is what the smoke test asks.
	Question = "In one sentence: what does the kubelet do when a container's liveness probe keeps failing?"
)

//go:embed smoke.py
var smokeScript string

// SmokeResult is the Job's one line of output.
type SmokeResult struct {
	Model                 string  `json:"model"`
	Answer                string  `json:"answer"`
	LatencySeconds        float64 `json:"latencySeconds"`
	LoadSeconds           float64 `json:"loadSeconds"`
	PromptTokens          int     `json:"promptTokens"`
	PromptTokensPerSecond float64 `json:"promptTokensPerSecond"`
	OutputTokens          int     `json:"outputTokens"`
	OutputTokensPerSecond float64 `json:"outputTokensPerSecond"`
	TraceID               string  `json:"traceId"`
	TraceStatus           int     `json:"traceStatus"`
}

// SmokeJob is the Job, labelled so the NetworkPolicy lets it reach Ollama.
func SmokeJob(name, model string) *batchv1.Job {
	labels := map[string]string{OllamaLabel: "true", "app.kubernetes.io/name": "lab-ai-smoke"}
	key := func(k string) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: AppSecret}, Key: k}}
	}
	backoff, ttl, deadline := int32(0), int32(600), int64(300)
	nonRoot, noEscalation := true, false
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoff, TTLSecondsAfterFinished: &ttl, ActiveDeadlineSeconds: &deadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: &noEscalation,
					SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: &nonRoot, RunAsUser: ptr(int64(65534)),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
					Containers: []corev1.Container{{
						Name: "smoke", Image: SmokeImage,
						Command: []string{"python3", "-I", "-c", smokeScript},
						Env: []corev1.EnvVar{
							{Name: "MODEL", Value: model},
							{Name: "QUESTION", Value: Question},
							{Name: "LANGFUSE_PUBLIC_KEY", ValueFrom: key("public-key")},
							{Name: "LANGFUSE_SECRET_KEY", ValueFrom: key("secret-key")},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceCPU: mustQty("10m"), corev1.ResourceMemory: mustQty("32Mi")},
							Limits:   corev1.ResourceList{corev1.ResourceMemory: mustQty("64Mi")},
						},
						SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &noEscalation,
							Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
					}},
				},
			},
		},
	}
}

// RunSmoke runs the smoke Job once, waits up to timeout for it to finish,
// and returns its result. The Job is deleted afterwards either way.
func RunSmoke(ctx context.Context, core kubernetes.Interface, model string, timeout time.Duration, log io.Writer) (SmokeResult, error) {
	name := fmt.Sprintf("lab-ai-smoke-%d", time.Now().Unix())
	jobs := core.BatchV1().Jobs(Namespace)
	if _, err := jobs.Create(ctx, SmokeJob(name, model), metav1.CreateOptions{}); err != nil {
		return SmokeResult{}, fmt.Errorf("create Job %s/%s: %w", Namespace, name, err)
	}
	_, _ = fmt.Fprintf(log, "Job %s/%s asks %s: %q\n", Namespace, name, model, Question)
	defer func() {
		bg := metav1.DeletePropagationBackground
		_ = jobs.Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{PropagationPolicy: &bg})
	}()
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	lw := cache.NewListWatchFromClient(core.BatchV1().RESTClient(), "jobs", Namespace, fields.OneTermEqualSelector("metadata.name", name))
	ev, err := watchtools.UntilWithSync(wctx, lw, &batchv1.Job{}, nil, func(e watch.Event) (bool, error) {
		j, ok := e.Object.(*batchv1.Job)
		if !ok {
			return false, nil
		}
		return j.Status.Succeeded > 0 || j.Status.Failed > 0, nil
	})
	if err != nil {
		return SmokeResult{}, fmt.Errorf("job %s did not finish in %s: %w", name, timeout, err)
	}
	job := ev.Object.(*batchv1.Job)
	out, logErr := jobLog(ctx, core, name)
	if job.Status.Failed > 0 {
		return SmokeResult{}, fmt.Errorf("job %s failed: %s", name, lastLines(out, 5, logErr))
	}
	if logErr != nil {
		return SmokeResult{}, logErr
	}
	return ParseSmoke(out)
}

// ParseSmoke reads the Job's last line of output.
func ParseSmoke(out string) (SmokeResult, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var r SmokeResult
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &r); err != nil {
		return SmokeResult{}, fmt.Errorf("smoke output %q: %w", lastLines(out, 3, nil), err)
	}
	switch {
	case r.Answer == "" || r.OutputTokens == 0:
		return r, errors.New("the model returned no answer")
	case r.TraceStatus != http.StatusOK:
		return r, fmt.Errorf("langfuse answered HTTP %d to the trace", r.TraceStatus)
	}
	return r, nil
}

func jobLog(ctx context.Context, core kubernetes.Interface, job string) (string, error) {
	pods, err := core.CoreV1().Pods(Namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + job})
	if err != nil {
		return "", err
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("job %s has no pod", job)
	}
	b, err := core.CoreV1().Pods(Namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{}).DoRaw(ctx)
	return string(b), err
}

func lastLines(s string, n int, err error) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := strings.Join(lines, " | ")
	if err != nil {
		out += " (" + err.Error() + ")"
	}
	return out
}

// Trace is what Langfuse holds for a trace: its observations.
type Trace struct {
	Observations []struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Model   string `json:"providedModelName"`
		TraceID string `json:"traceId"`
	} `json:"data"`
}

// FindTrace asks Langfuse at baseURL (v4 API, /api/public/v2/observations)
// for a trace's observations with the project's keys from the
// langfuse-credentials Secret. Langfuse writes events in batches, so the caller
// retries.
func FindTrace(ctx context.Context, core kubernetes.Interface, baseURL, traceID string) (Trace, error) {
	s, err := core.CoreV1().Secrets(Namespace).Get(ctx, AppSecret, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Trace{}, fmt.Errorf("secret %s/%s not found: is the ai stack up?", Namespace, AppSecret)
	}
	if err != nil {
		return Trace{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/public/v2/observations?traceId="+traceID, nil)
	if err != nil {
		return Trace{}, err
	}
	auth := base64.StdEncoding.EncodeToString([]byte(string(s.Data["public-key"]) + ":" + string(s.Data["secret-key"])))
	req.Header.Set("Authorization", "Basic "+auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Trace{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Trace{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Trace{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}
	var t Trace
	return t, json.Unmarshal(b, &t)
}

func ptr[T any](v T) *T { return &v }

func mustQty(s string) resource.Quantity { return resource.MustParse(s) }
