#!/usr/bin/env bash
# Step 5: run the debug Pod, then read its PolicyReport.
set -euo pipefail
kubectl apply -f "$(dirname "$0")/05-debug.yaml"
kubectl -n lab05-apps wait --for=condition=Ready pod/debug --timeout=180s
for _ in $(seq 30); do
  kubectl -n lab05-apps get policyreports -o json |
    jq -e '.items[] | select(.scope.name == "debug") | .results[] | select(.policy == "disallow-host-path" and .result == "fail")' >/dev/null && break
  sleep 4
done
kubectl -n lab05-apps get policyreports -o wide
