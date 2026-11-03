#!/usr/bin/env bash
# Step 5: Pod debug mounts a hostPath, which the baseline policies only
# audit: it runs, and its PolicyReport records the failure.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_kyverno
[ "$(kubectl -n "$NS" get pod debug -o jsonpath='{.status.phase}' 2>/dev/null)" = Running ] || fail "Pod debug is not running in $NS"
kubectl -n "$NS" get pod debug -o json | jq -e '.spec.volumes | any(.hostPath)' >/dev/null || fail "Pod debug has no hostPath volume"
r=$(kubectl -n "$NS" get policyreports -o json 2>/dev/null |
  jq -r '[.items[] | select(.scope.kind == "Pod" and .scope.name == "debug") | .results[]? | select(.policy == "disallow-host-path" and .result == "fail")] | length')
[ "${r:-0}" -gt 0 ] || fail "no PolicyReport result fail for disallow-host-path on Pod debug yet: reports take up to a minute"
ok "Pod debug runs, and its PolicyReport says disallow-host-path: fail"
