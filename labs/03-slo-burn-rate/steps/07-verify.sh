#!/usr/bin/env bash
# Step 7: Sloth's rules for the same SLO are loaded from lab03-slo: its
# recording rules and its multiwindow page alert.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_prometheus
r=$(rules)
n=$(jq '[.[] | select(.labels.sloth_service == "lab03-podinfo")] | length' <<<"$r")
[ "${n:-0}" -gt 0 ] || fail "Prometheus has no rules with sloth_service=\"lab03-podinfo\": generate them with Sloth and apply the PrometheusRule to lab03-slo"
jq -e 'any(.[]; .name == "slo:sli_error:ratio_rate5m" and .labels.sloth_service == "lab03-podinfo")' <<<"$r" >/dev/null ||
  fail "Sloth's recording rule slo:sli_error:ratio_rate5m is missing"
page=$(jq -r '[.[] | select(.type == "alerting" and .labels.severity == "page" and (.query | test("sloth_service=\"lab03-podinfo\"")))][0].query // ""' <<<"$r")
[ -n "$page" ] || fail "Sloth's page alert (severity=page) for lab03-podinfo is missing"
if ! grep -q '14.4' <<<"$page" || ! grep -q '(6 \*' <<<"$page"; then
  fail "Sloth's page alert does not use the 14.4 and 6 burn rates"
fi
ok "Sloth's rules for lab03-podinfo are loaded: $n rules, a page alert at 14.4 and 6"
