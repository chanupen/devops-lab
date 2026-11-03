#!/usr/bin/env bash
# Step 3: Pod web runs in lab05-apps with the image by digest.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_kyverno
img=$(kubectl -n "$NS" get pod web -o jsonpath='{.spec.containers[0].image}' 2>/dev/null)
[ -n "$img" ] || fail "no Pod web in $NS"
[ "$img" = "$DIGEST_IMAGE" ] || fail "Pod web uses '$img', want $DIGEST_IMAGE"
[ "$(kubectl -n "$NS" get pod web -o jsonpath='{.status.phase}')" = Running ] || fail "Pod web is not Running yet"
ok "Pod web runs $DIGEST_IMAGE"
