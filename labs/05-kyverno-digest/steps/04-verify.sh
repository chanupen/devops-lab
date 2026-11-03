#!/usr/bin/env bash
# Step 4: one named workload, Deployment legacy, runs a tag-only image under
# a PolicyException; any other tag-only Pod is still denied, even one that
# copies legacy's labels.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_kyverno
e=$(kubectl -n "$NS" get policyexceptions.policies.kyverno.io legacy-tag -o json 2>/dev/null) || fail "no PolicyException legacy-tag (policies.kyverno.io) in $NS"
jq -e --arg p "$POLICY" '.spec.policyRefs | any(.name == $p and .kind == "ValidatingPolicy")' <<<"$e" >/dev/null || fail "PolicyException legacy-tag does not name ValidatingPolicy $POLICY in policyRefs"
jq -e '.spec.matchConditions // [] | length > 0' <<<"$e" >/dev/null || fail "PolicyException legacy-tag must match the workload (matchConditions), not everything"
img=$(kubectl -n "$NS" get deployment legacy -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null)
[ "$img" = "$TAG_IMAGE" ] || fail "Deployment legacy must run $TAG_IMAGE (by tag), it runs '$img'"
[ "$(kubectl -n "$NS" get deployment legacy -o jsonpath='{.status.availableReplicas}')" = 1 ] || fail "Deployment legacy is not available yet"
out=$(dry_run_pod other "$TAG_IMAGE")
grep -q "pinned by digest" <<<"$out" || fail "the exception is too wide: Pod other with a tag-only image is allowed too"
out=$(kubectl -n "$NS" run copycat --labels=app=legacy --image="$TAG_IMAGE" --restart=Never --dry-run=server -o name 2>&1)
grep -q "pinned by digest" <<<"$out" || fail "the exception is too wide: a Pod that only copies legacy's labels (app=legacy) is allowed"
ok "Deployment legacy runs by tag under PolicyException legacy-tag; other Pods are still denied"
