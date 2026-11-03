#!/usr/bin/env bash
# Step 1: ClusterPolicy require-image-digest enforces images by digest on
# Pods in lab05-* namespaces, carries the lab label, and is ready.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_kyverno
p=$(policy) || fail "no ClusterPolicy $POLICY"
[ "$(jq -r '.metadata.labels["lab.devops/lab"] // ""' <<<"$p")" = 05-kyverno-digest ] || fail "ClusterPolicy $POLICY needs the label $LABEL"
jq -e '.spec.rules | any(.validate.failureAction == "Enforce")' <<<"$p" >/dev/null || fail "no rule of $POLICY has validate.failureAction: Enforce (Audit only reports)"
jq -e '.spec.rules | any(.match.any[]?.resources.namespaces[]? == "lab05-*")' <<<"$p" >/dev/null || fail "$POLICY must match only namespaces lab05-*"
jq -e '[.spec.rules[].match.any[]?.resources.namespaces[]?] | all(startswith("lab05-"))' <<<"$p" >/dev/null || fail "$POLICY matches namespaces outside lab05-*"
jq -e '.spec.rules | any(.validate.pattern | tostring | test("@sha256"))' <<<"$p" >/dev/null || fail "$POLICY must require *@sha256:* images in its pattern"
[ "$(jq -r '.status.conditions[]? | select(.type == "Ready") | .status' <<<"$p")" = True ] || fail "ClusterPolicy $POLICY is not ready yet: kubectl get clusterpolicy $POLICY"
ok "ClusterPolicy $POLICY enforces digests in lab05-*"
