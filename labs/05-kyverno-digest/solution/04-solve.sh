#!/usr/bin/env bash
# Step 4: the exception first, then the workload it allows.
set -euo pipefail
kubectl apply -f "$(dirname "$0")/04-exception.yaml"
kubectl -n lab05-apps rollout status deployment/legacy --timeout=180s
