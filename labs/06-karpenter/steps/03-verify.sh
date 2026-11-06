#!/usr/bin/env bash
# Step 3: inflate is down to 2 pods, and consolidation left one node.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
need_cap
inflate_ready 2
need_steady
n=$(pool_nodes | grep -c .)
[ "$n" -eq 1 ] || fail "NodePool $POOL still has $n nodes for 2 pods that fit on one: wait for consolidation (consolidateAfter is 30s), then verify again"
node=$(pool_nodes)
[ "$(pods_per_node)" = "$node 2" ] || fail "the 2 inflate pods are not both on $node: $(pods_per_node | tr '\n' ' ')"
ok "consolidation left one node, $node, with both pods"
