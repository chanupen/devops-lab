#!/usr/bin/env bash
# Step 3: create the Application from YAML, sync it and wait until healthy.
set -euo pipefail
cd "$(dirname "$0")"
kubectl apply -f 03-podinfo-app.yaml
argocd app sync podinfo
argocd app wait podinfo --sync --health --timeout 300
