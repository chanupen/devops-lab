#!/usr/bin/env bash
# Step 4: back to 4 pods on 2 nodes, a PDB that allows no disruption, then
# down to 2 pods kept one per node (pod-deletion-cost tells the ReplicaSet
# which pods to delete first). Karpenter wants to merge the two half-empty
# nodes and cannot evict either pod.
set -euo pipefail
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
kubectl -n "$NS" scale deployment inflate --replicas=4
wait_step 2 8
kubectl apply -f "$(dirname "$0")/04-pdb.yaml"
for node in $(pool_nodes); do
  pod=$(kubectl -n "$NS" get pods -l app=inflate --field-selector "spec.nodeName=$node" -o jsonpath='{.items[0].metadata.name}')
  kubectl -n "$NS" annotate pod "$pod" --overwrite controller.kubernetes.io/pod-deletion-cost=1000
done
kubectl -n "$NS" scale deployment inflate --replicas=2
wait_step 4 6
