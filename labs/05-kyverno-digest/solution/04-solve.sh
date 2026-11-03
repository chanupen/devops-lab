#!/usr/bin/env bash
# Step 4: the exception first, then the workload it allows. Kyverno takes a
# few seconds to load a new exception, so wait until a dry run passes.
set -euo pipefail
here="$(dirname "$0")"
kubectl apply -f "$here/04-exception.yaml"
for _ in $(seq 30); do
  kubectl apply --dry-run=server -f "$here/04-legacy.yaml" >/dev/null 2>&1 && break
  sleep 2
done
kubectl apply -f "$here/04-legacy.yaml"
kubectl -n lab05-apps rollout status deployment/legacy --timeout=180s
