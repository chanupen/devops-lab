#!/usr/bin/env bash
# Step 6: the prod Deployment records the digest that was scanned, and the
# pods in lab02-dev and lab02-prod run exactly that digest.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_registry
need_cluster
scanned=$(kubectl -n lab02-prod get deployment hello -o jsonpath='{.metadata.annotations.lab\.devops/scanned-digest}' 2>/dev/null)
[ -n "$scanned" ] || fail "Deployment hello in lab02-prod has no annotation lab.devops/scanned-digest: record the digest you scanned"
[ "$(base_layer "$scanned" 2>/dev/null)" = "$FIXED_BASE" ] || fail "the recorded digest $scanned is not the patched image you scanned clean in step 3"
for ns in lab02-dev lab02-prod; do
  r=$(running $ns)
  [ -n "$r" ] || fail "no running hello pod in $ns"
  [ "$r" = "$scanned" ] || fail "the pods in $ns run $(echo "$r" | tr '\n' ' '), not the scanned digest $scanned"
done
ok "dev and prod both run $scanned, the digest that was scanned"
