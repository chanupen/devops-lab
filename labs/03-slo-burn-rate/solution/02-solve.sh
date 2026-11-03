#!/usr/bin/env bash
# Step 2: apply the PrometheusRule; the operator loads it into Prometheus.
set -euo pipefail
kubectl apply -f "$(dirname "$0")/02-rules.yaml"
