{{/* Sync policy shared by every lab Application. */}}
{{- define "lab.syncPolicy" -}}
automated:
  prune: true
  selfHeal: true
retry:
  limit: 10
  backoff:
    duration: 10s
    factor: 2
    maxDuration: 3m
{{- end }}

{{/* OCI chart repository, through the mirror when one is set; pass (list . "ghcr.io/org/charts"). */}}
{{- define "lab.ociRepo" -}}
{{- $root := index . 0 }}{{ $upstream := index . 1 -}}
{{- with $root.Values.charts.ociMirror }}{{ . }}/{{ end }}{{ $upstream }}
{{- end }}

{{/* OCI chart repositories the lab's apps use; each is registered with Argo CD. */}}
{{- define "lab.ociUpstreams" -}}
- quay.io/jetstack/charts
- docker.io/envoyproxy
- ghcr.io/prometheus-community/charts
- ghcr.io/grafana/helm-charts
- ghcr.io/stefanprodan/charts
- ghcr.io/open-telemetry/opentelemetry-helm-charts
- ghcr.io/kyverno/charts
{{- end }}

{{/* Metadata shared by every lab Application; pass (list . "name" "stack" "wave"). */}}
{{- define "lab.appMeta" -}}
{{- $name := index . 1 }}{{ $stack := index . 2 }}{{ $wave := index . 3 -}}
name: {{ $name }}
namespace: argocd
labels:
  lab.stack: {{ $stack }}
annotations:
  argocd.argoproj.io/sync-wave: {{ $wave | quote }}
finalizers:
  - resources-finalizer.argocd.argoproj.io
{{- end }}
