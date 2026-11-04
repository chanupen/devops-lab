#!/usr/bin/env bash
# The reference fix: find who scaled CoreDNS and to what, then put the
# replicas back. The managed fields name the field manager that set
# spec.replicas (kube-downscaler); its annotation holds the replicas
# before. In a real cluster the lasting fix is to exclude kube-system
# from the downscaler, and an alert on CoreDNS being gone.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
kubectl -n kube-system get deployment coredns -o json --show-managed-fields |
  jq -r '.metadata.managedFields[] | select(.fieldsV1."f:spec"."f:replicas") | "spec.replicas set by \(.manager) at \(.time)"'
want=$(kubectl -n kube-system get deployment coredns -o jsonpath='{.metadata.annotations.downscaler/original-replicas}')
kubectl -n kube-system scale deployment coredns --replicas="${want:-2}"
kubectl -n kube-system rollout status deployment coredns --timeout=120s
