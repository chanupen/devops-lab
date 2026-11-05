#!/usr/bin/env bash
# The reference fix: find the NotReady node, see its container stopped,
# start it and wait until it is Ready and the app serves. What should have
# prevented the outage (two or more replicas spread across nodes; a PDB
# only covers voluntary disruptions) is in the README.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
kubectl get nodes
down=$(kubectl get nodes -o json | jq -r '.items[] | select(any(.status.conditions[]; .type == "Ready" and .status != "True")) | .metadata.name')
[ -n "$down" ] || ok "every node is Ready"
podman ps -a --filter label=io.x-k8s.kind.cluster=lab --format '{{.Names}} {{.Status}}'
for n in $down; do
  podman start "$n"
  wait_for 180 "node $n to be Ready" node_ready "$n"
done
kubectl -n podinfo rollout status deployment podinfo-frontend --timeout=180s
kubectl -n podinfo rollout status deployment podinfo-backend --timeout=180s
kubectl get nodes
