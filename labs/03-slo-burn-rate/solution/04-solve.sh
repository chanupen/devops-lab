#!/usr/bin/env bash
# Step 4: apply the PrometheusRule; the operator loads it into Prometheus.
set -euo pipefail
kubectl apply -f "$(dirname "$0")/04-rules.yaml"
