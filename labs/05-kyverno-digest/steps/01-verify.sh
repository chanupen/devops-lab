#!/usr/bin/env bash
# Step 1: ValidatingPolicy require-image-digest denies Pods in lab05-*
# namespaces whose images are not pinned by digest, carries the lab label,
# and is ready.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_kyverno
p=$(policy) || fail "no ValidatingPolicy $POLICY"
[ "$(jq -r '.metadata.labels["lab.devops/lab"] // ""' <<<"$p")" = 05-kyverno-digest ] || fail "ValidatingPolicy $POLICY needs the label $LABEL"
jq -e '.spec.validationActions // [] | index("Deny")' <<<"$p" >/dev/null || fail "$POLICY needs validationActions: [Deny] (Audit only reports)"
jq -e '.spec.matchConstraints.resourceRules // [] | any(.resources | index("pods"))' <<<"$p" >/dev/null || fail "$POLICY must match pods in matchConstraints.resourceRules"
jq -e '.spec.matchConditions // [] | any(.expression | test("lab05-"))' <<<"$p" >/dev/null || fail "$POLICY must apply only to lab05-* namespaces (a matchCondition on the namespace)"
jq -e '.spec.validations // [] | any(.expression | test("@sha256"))' <<<"$p" >/dev/null || fail "$POLICY must check that images contain @sha256:"
[ "$(jq -r '.status.conditionStatus.ready // false' <<<"$p")" = true ] || fail "ValidatingPolicy $POLICY is not ready yet: kubectl get vpol $POLICY"
ok "ValidatingPolicy $POLICY denies tag-only images in lab05-*"
