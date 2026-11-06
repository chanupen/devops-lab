#!/usr/bin/env bash
# Step 5: the PDB allows one pod to go at a time, and consolidation moved
# the pods back onto one node.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
need_cap
pdb=$(kubectl -n "$NS" get pdb -o json 2>/dev/null | jq -r '.items[] | select(.spec.selector.matchLabels.app == "inflate") | .metadata.name' | head -1)
[ -n "$pdb" ] || fail "no PodDisruptionBudget in $NS selects app=inflate: keep it, and let it allow one disruption"
allowed=$(kubectl -n "$NS" get pdb "$pdb" -o jsonpath='{.status.disruptionsAllowed}')
[ "${allowed:-0}" -ge 1 ] || fail "PDB $pdb still allows no disruption: set maxUnavailable: 1"
inflate_ready 2
need_steady
n=$(pool_nodes | grep -c .)
[ "$n" -eq 1 ] || fail "NodePool $POOL still has $n nodes: wait for consolidation, then verify again"
node=$(pool_nodes)
[ "$(pods_per_node)" = "$node 2" ] || fail "the 2 inflate pods are not both on $node: $(pods_per_node | tr '\n' ' ')"
ok "PDB $pdb allows $allowed disruption(s), and Karpenter consolidated onto $node"
