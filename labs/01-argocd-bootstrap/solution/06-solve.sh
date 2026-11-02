#!/usr/bin/env bash
# Step 6: the root Application, then wait for both children to sync.
set -euo pipefail
cd "$(dirname "$0")"
kubectl apply -f 06-root-app.yaml
for app in root example.guestbook example.helm-guestbook; do
  for _ in $(seq 60); do
    kubectl -n argocd get application "$app" >/dev/null 2>&1 && break
    sleep 2
  done
  kubectl -n argocd wait application "$app" --for=jsonpath='{.status.sync.status}'=Synced --timeout=180s
done
kubectl -n argocd get applications
