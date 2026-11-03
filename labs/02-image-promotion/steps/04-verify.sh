#!/usr/bin/env bash
# Step 4: Deployment hello in lab02-dev runs the dev image by its digest.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
d=$(tag_digest dev)
[ -n "$d" ] || fail "the registry has no $PUSH/$REPO:dev"
img=$(deployed lab02-dev)
[ -n "$img" ] || fail "no Deployment hello in namespace lab02-dev"
case "$img" in
  *@sha256:*) ;;
  *) fail "Deployment hello in lab02-dev uses '$img', a tag: use the digest, $PULL/$REPO@$d" ;;
esac
[ "$img" = "$PULL/$REPO@$d" ] || fail "Deployment hello in lab02-dev runs '$img', not the dev digest $PULL/$REPO@$d"
ready lab02-dev || fail "Deployment hello in lab02-dev is not available yet: kubectl -n lab02-dev rollout status deployment/hello"
ok "lab02-dev runs $PULL/$REPO@$d"
