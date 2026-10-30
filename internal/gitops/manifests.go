package gitops

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/chanupen/devops-lab/internal/config"
)

// appHealthLua lets an Application's health follow its children's, so the
// app-of-apps honours sync waves and reports Healthy only when they are.
const appHealthLua = `hs = {}
hs.status = "Progressing"
hs.message = ""
if obj.status ~= nil and obj.status.health ~= nil then
  hs.status = obj.status.health.status
  if obj.status.health.message ~= nil then
    hs.message = obj.status.health.message
  end
end
return hs
`

// argoValues are the Argo CD chart values. Dex and notifications are off; the
// UI is reached with a port-forward, so the server runs without TLS.
func argoValues() map[string]any {
	return map[string]any{
		"dex":           map[string]any{"enabled": false},
		"notifications": map[string]any{"enabled": false},
		// The chart's default registry (ecr-public) is not mirrored by the
		// local cache; the same image from docker.io is.
		"redis": map[string]any{"image": map[string]any{"repository": "docker.io/library/redis"}},
		"configs": map[string]any{
			"params": map[string]any{"server.insecure": true},
			"cm": map[string]any{
				"resource.customizations.health.argoproj.io_Application": appHealthLua,
				"timeout.reconciliation":                                 "60s",
			},
		},
	}
}

// repoSecret is the Argo CD repository credential for the GitOps repo.
func repoSecret(url string, key []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: RepoSecret, Namespace: Namespace,
			Labels: map[string]string{"argocd.argoproj.io/secret-type": "repository", "lab": "true"},
		},
		StringData: map[string]string{"type": "git", "url": url, "sshPrivateKey": string(key)},
	}
}

// rootApp is the app-of-apps: the bootstrap chart at repo.Revision, with the
// mode, repo and enabled stacks as values.
func rootApp(mode string, repo config.Repo, stacks map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata": map[string]any{
			"name":       RootApp,
			"namespace":  Namespace,
			"labels":     map[string]any{stackLabel: "gitops"},
			"finalizers": []any{"resources-finalizer.argocd.argoproj.io"},
		},
		"spec": map[string]any{
			"project": "default",
			"source": map[string]any{
				"repoURL":        repo.URL,
				"targetRevision": repo.Revision,
				"path":           BootstrapPath,
				"helm": map[string]any{"valuesObject": map[string]any{
					"mode":   mode,
					"repo":   map[string]any{"url": repo.URL, "revision": repo.Revision},
					"stacks": stacks,
				}},
			},
			"destination": map[string]any{"server": "https://kubernetes.default.svc", "namespace": Namespace},
			"syncPolicy": map[string]any{
				// allowEmpty: with every stack off the chart renders nothing, and
				// Argo CD refuses to auto-sync to an empty set without it.
				"automated": map[string]any{"prune": true, "selfHeal": true, "allowEmpty": true},
				"retry": map[string]any{"limit": int64(10), "backoff": map[string]any{
					"duration": "10s", "factor": int64(2), "maxDuration": "3m",
				}},
			},
		},
	}}
}
