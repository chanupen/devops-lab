#!/usr/bin/env bash
# Step 1: read the cluster's NodePool spot, record two of its settings, and
# apply the lab's NodePool.
set -euo pipefail
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
kubectl get nodepool spot -o yaml
kubectl apply -f "$APP/namespace.yaml" -f "$APP/nodepool.yaml"
kubectl -n "$NS" create configmap answers --dry-run=client -o yaml \
  --from-literal=cpu-limit="$(kubectl get nodepool spot -o jsonpath='{.spec.limits.cpu}')" \
  --from-literal=consolidate-after="$(kubectl get nodepool spot -o jsonpath='{.spec.disruption.consolidateAfter}')" |
  kubectl apply -f -
wait_step 1 2
