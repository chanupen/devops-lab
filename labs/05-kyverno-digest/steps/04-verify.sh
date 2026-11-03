#!/usr/bin/env bash
# Step 4: one named workload, Deployment legacy, runs a tag-only image under
# a PolicyException; any other tag-only Pod is still denied.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_kyverno
e=$(kubectl -n "$NS" get policyexceptions.kyverno.io legacy-tag -o json 2>/dev/null) || fail "no PolicyException legacy-tag in $NS"
jq -e --arg p "$POLICY" '.spec.exceptions | any(.policyName == $p)' <<<"$e" >/dev/null || fail "PolicyException legacy-tag does not name policy $POLICY"
names=$(jq -r '[.spec.match.any[]?.resources.names[]?] | join(" ")' <<<"$e")
[ -n "$names" ] || fail "PolicyException legacy-tag must name the workload (resources.names), not match everything"
img=$(kubectl -n "$NS" get deployment legacy -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null)
[ "$img" = "$TAG_IMAGE" ] || fail "Deployment legacy must run $TAG_IMAGE (by tag), it runs '$img'"
[ "$(kubectl -n "$NS" get deployment legacy -o jsonpath='{.status.availableReplicas}')" = 1 ] || fail "Deployment legacy is not available yet"
out=$(dry_run_pod other "$TAG_IMAGE")
grep -q "pinned by digest" <<<"$out" || fail "the exception is too wide: Pod other with a tag-only image is allowed too"
ok "Deployment legacy runs by tag under PolicyException legacy-tag; other Pods are still denied"
