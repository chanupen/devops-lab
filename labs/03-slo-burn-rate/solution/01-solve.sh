#!/usr/bin/env bash
# Step 1: steady traffic to podinfo-frontend, then look at the SLI's metric.
set -euo pipefail
kubectl apply -f "$(dirname "$0")/01-load.yaml"
kubectl -n lab03-load rollout status deployment/load --timeout=180s
