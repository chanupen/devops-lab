#!/usr/bin/env bash
# Back to the start of lab 6: no namespace lab06-karpenter (inflate, its
# PDB, the answers) and no NodePool lab06. Deleting the NodePool makes
# Karpenter drain and terminate its nodes; reset waits until they are gone,
# so nothing of the lab is left billed.
set -uo pipefail
# shellcheck source=SCRIPTDIR/steps/lib.sh
. "$(dirname "$0")/steps/lib.sh"
kubectl delete namespace "$NS" --ignore-not-found --wait --timeout=120s
kubectl delete nodepool -l "$LABEL" --ignore-not-found --wait=false
kubectl delete nodepool "$POOL" --ignore-not-found --wait=false
if ! kubectl wait nodeclaims -l "karpenter.sh/nodepool=$POOL" --for=delete --timeout=240s 2>/dev/null; then
  left=$(kubectl get nodeclaims -l "karpenter.sh/nodepool=$POOL" -o name 2>/dev/null)
  [ -z "$left" ] || { echo "Karpenter has not removed $(tr '\n' ' ' <<<"$left")yet: kubectl get nodeclaims"; exit 1; }
fi
echo "lab 6 is back at its start: no NodePool $POOL, no nodes of it, no namespace $NS"
