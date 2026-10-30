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
