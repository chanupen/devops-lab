#!/usr/bin/env bash
# Step 2: namespace lab05-apps exists, and a Pod with a tag-only image is
# denied there (checked with a server-side dry run, which stores nothing).
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_kyverno
kubectl get namespace "$NS" >/dev/null 2>&1 || fail "no namespace $NS"
out=$(dry_run_pod tag-check "$TAG_IMAGE")
grep -q "pinned by digest" <<<"$out" || fail "a Pod with $TAG_IMAGE is not denied in $NS: $(head -1 <<<"$out")"
ok "a tag-only Pod is denied in $NS"
