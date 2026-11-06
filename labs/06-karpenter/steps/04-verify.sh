#!/usr/bin/env bash
# Step 4: a PDB that allows no disruption keeps 2 half-empty nodes: the
# pods sit one per node, and Karpenter cannot evict either to consolidate.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
need_cap
pdb=$(kubectl -n "$NS" get pdb -o json 2>/dev/null | jq -r '.items[] | select(.spec.selector.matchLabels.app == "inflate") | .metadata.name' | head -1)
[ -n "$pdb" ] || fail "no PodDisruptionBudget in $NS selects app=inflate"
allowed=$(kubectl -n "$NS" get pdb "$pdb" -o jsonpath='{.status.disruptionsAllowed}')
[ "$allowed" = 0 ] || fail "PDB $pdb allows ${allowed:-?} disruption(s); for this step it must allow none (maxUnavailable: 0)"
inflate_ready 2
need_steady
n=$(pool_nodes | grep -c .)
[ "$n" -eq 2 ] || fail "NodePool $POOL has $n node(s), want 2 with one pod each: scale to 4, then back to 2 keeping one pod per node"
per=$(pods_per_node)
[ "$(awk '$2 == 1' <<<"$per" | grep -c .)" -eq 2 ] || fail "the pods are not one per node: $(tr '\n' ' ' <<<"$per")(hint: controller.kubernetes.io/pod-deletion-cost)"
# Karpenter wanted to consolidate and could not: a DisruptionBlocked event
# that names this PDB, on one of the pool's nodes or NodeClaims.
nodes=$(pool_nodes)
claimed=$(kubectl get nodeclaims -l "karpenter.sh/nodepool=$POOL" -o json | jq -r '.items[] | "\(.metadata.name) \(.status.nodeName)"')
blocked=$(events DisruptionBlocked | grep -F "PodDisruptionBudget=[$NS/$pdb]" | while read -r _ name _; do
  grep -qw -- "$name" <<<"$nodes $claimed" && echo "$name"
done | head -1)
[ -n "$blocked" ] || fail "Karpenter has not tried to consolidate yet (no DisruptionBlocked event naming PDB $pdb): wait a minute and verify again"
ok "PDB $pdb allows no disruption, so Karpenter keeps 2 nodes with one pod each (DisruptionBlocked on $blocked)"
