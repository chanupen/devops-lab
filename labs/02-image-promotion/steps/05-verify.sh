#!/usr/bin/env bash
# Step 5: lab02-hello:prod is the same digest as dev (copied, not rebuilt),
# and Deployment hello in lab02-prod runs it by that digest.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_registry
need_cluster
dev=$(tag_digest dev)
prod=$(tag_digest prod)
[ -n "$prod" ] || fail "the registry has no $PUSH/$REPO:prod: copy the dev digest to the prod tag with skopeo"
[ "$prod" = "$dev" ] || fail "$REPO:prod is $prod but dev is $dev: promote by copying the dev digest, never by rebuilding"
img=$(deployed lab02-prod)
[ -n "$img" ] || fail "no Deployment hello in namespace lab02-prod"
[ "$img" = "$PULL/$REPO@$prod" ] || fail "Deployment hello in lab02-prod runs '$img', not $PULL/$REPO@$prod"
ready lab02-prod || fail "Deployment hello in lab02-prod is not available yet: kubectl -n lab02-prod rollout status deployment/hello"
ok "prod is the dev digest $prod, and lab02-prod runs it"
