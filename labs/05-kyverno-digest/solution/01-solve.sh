#!/usr/bin/env bash
# Step 1: apply the ClusterPolicy and wait until Kyverno reports it ready.
set -euo pipefail
kubectl apply -f "$(dirname "$0")/01-policy.yaml"
kubectl wait --for=condition=Ready clusterpolicy/require-image-digest --timeout=120s
