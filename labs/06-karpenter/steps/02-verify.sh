#!/usr/bin/env bash
# Step 2: inflate runs 4 pods, all on spot nodes of NodePool lab06, which
# Karpenter launched for them (at least two nodes).
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
need_cap
inflate_ready 4
need_steady
nodes=$(pool_nodes)
n=$(grep -c . <<<"$nodes")
[ "$n" -ge 2 ] || fail "NodePool $POOL has $n node(s); 4 pods of $POD_CPU need at least 2: is the pod request still $POD_CPU?"
while read -r node _; do
  grep -qx "$node" <<<"$nodes" || fail "an inflate pod runs on $node, which is not a node of $POOL"
done < <(pods_per_node)
od=$(kubectl get nodes -l "karpenter.sh/nodepool=$POOL,karpenter.sh/capacity-type!=spot" -o name 2>/dev/null)
[ -z "$od" ] || fail "$POOL has nodes that are not spot: $od"
types=$(kubectl get nodes -l "karpenter.sh/nodepool=$POOL" -o jsonpath='{range .items[*]}{.metadata.labels.node\.kubernetes\.io/instance-type}{" "}{end}')
ok "4 inflate pods run on $n spot nodes of $POOL ($types)"
