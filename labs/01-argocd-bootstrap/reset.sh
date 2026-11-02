#!/usr/bin/env bash
# Back to the start of lab 1: no Argo CD, no apps, an empty cluster.
# lab labs reset runs it with KUBECONFIG set to the lab's cluster.
set -uo pipefail
# Delete the Applications while Argo CD still runs, so it removes what they
# deployed (the root first: its children go with it).
kubectl -n argocd delete application root --ignore-not-found --wait --timeout=120s 2>/dev/null
kubectl -n argocd delete applications --all --wait --timeout=120s 2>/dev/null
# Anything left has a finalizer nobody will run now: drop it.
for a in $(kubectl -n argocd get applications -o name 2>/dev/null); do
  kubectl -n argocd patch "$a" --type json -p '[{"op":"remove","path":"/metadata/finalizers"}]' >/dev/null 2>&1
done
helm uninstall argocd -n argocd --wait --timeout 5m 2>/dev/null
# The chart keeps its CRDs on uninstall.
kubectl get crd -o name | grep '\.argoproj\.io$' | xargs -r kubectl delete --wait=false
kubectl delete namespace argocd podinfo guestbook helm-guestbook --ignore-not-found --wait --timeout=180s
# The CLI session pointed at this Argo CD.
argocd logout localhost:18080 >/dev/null 2>&1 || true
pidfile="${TMPDIR:-/tmp}/lab-01-port-forward.pid"
[ -f "$pidfile" ] && kill "$(cat "$pidfile")" 2>/dev/null && rm -f "$pidfile"
echo "lab 1 is back at its start: stop any port-forward you started by hand"
