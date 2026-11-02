#!/usr/bin/env bash
# Step 5: change the cluster by hand and watch Argo CD put it back.
set -euo pipefail
kubectl -n podinfo scale deployment podinfo --replicas=3
for _ in $(seq 60); do
  replicas=$(kubectl -n podinfo get deployment podinfo -o jsonpath='{.spec.replicas}')
  echo "replicas: $replicas"
  [ "$replicas" = 1 ] && exit 0
  sleep 2
done
echo "Argo CD did not put the Deployment back within 2 minutes"
exit 1
