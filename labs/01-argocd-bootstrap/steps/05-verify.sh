#!/usr/bin/env bash
# Step 5: the podinfo Deployment was changed by hand, and Argo CD put it
# back: 1 replica, as in Git.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
want=1
need_cluster
d=$(kubectl -n podinfo get deployment podinfo -o json 2>/dev/null) || fail "no Deployment podinfo in namespace podinfo: do step 3 first"
replicas=$(jq -r '.spec.replicas' <<<"$d")
generation=$(jq -r '.metadata.generation' <<<"$d")
[ "$replicas" = "$want" ] || fail "the Deployment has $replicas replicas, Git says $want: is self-heal on? Wait a few seconds and verify again"
# Created at generation 1; a hand-made scale and the revert make it 3 or more.
[ "$generation" -ge 3 ] ||
  fail "the replicas match Git, but nothing changed them yet: kubectl -n podinfo scale deployment podinfo --replicas=3, then watch"
ok "the Deployment was scaled by hand and is back to $want replica, as in Git"
