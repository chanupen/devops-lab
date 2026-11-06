#!/usr/bin/env bash
# Step 6: Karpenter received a spot interruption warning for a node of
# lab06, drained it, and inflate runs again on a replacement node.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
need_cap
# The interrupted NodeClaims of the pool (named lab06-...) and their nodes.
hit=$(interrupt_events | awk -v p="$POOL-" '($1 == "NodeClaim" && index($2, p) == 1) || $1 == "Node"')
[ -n "$hit" ] || fail "no interruption event yet: send the interruption warning for a node of $POOL to Karpenter's queue"
claimhit=$(awk '$1 == "NodeClaim"' <<<"$hit")
[ -n "$claimhit" ] || fail "the interruption events name no NodeClaim of $POOL: send the warning for a node of $POOL"
while read -r kind name; do
  case "$kind" in
    NodeClaim) kubectl get nodeclaim "$name" >/dev/null 2>&1 && fail "NodeClaim $name is still there: Karpenter is draining it; verify again in a minute" ;;
    Node) kubectl get node "$name" >/dev/null 2>&1 && fail "node $name is still there: Karpenter is draining it; verify again in a minute" ;;
  esac
done <<<"$hit"
inflate_ready 2
need_steady
nodes=$(pool_nodes)
[ -n "$nodes" ] || fail "NodePool $POOL has no node for inflate yet"
while read -r node _; do
  grep -qx "$node" <<<"$nodes" || fail "an inflate pod runs on $node, which is not a live node of $POOL"
done < <(pods_per_node)
ok "Karpenter drained the interrupted node ($(awk '{print $2}' <<<"$claimhit" | tr '\n' ' ')) and inflate runs on $(tr '\n' ' ' <<<"$nodes")"
