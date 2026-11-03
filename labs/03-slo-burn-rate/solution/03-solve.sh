#!/usr/bin/env bash
# Step 3: apply the PrometheusRule; the operator loads it into Prometheus.
set -euo pipefail
kubectl apply -f "$(dirname "$0")/03-rules.yaml"
