#!/usr/bin/env bash
# Step 6: LabSLOFastBurn fired and reached Alertmanager, and the page is
# acknowledged: an active silence for it, with a comment.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_prometheus
a=$(am_alerts) || fail "Alertmanager does not answer"
n=$(jq '[.[] | select(.labels.alertname == "LabSLOFastBurn")] | length' <<<"$a")
[ "$n" -gt 0 ] || fail "Alertmanager has no LabSLOFastBurn alert yet: it fires a minute or two after the errors start"
s=$(kubectl get --raw "$AM/api/v2/silences" --request-timeout=15s 2>/dev/null |
  jq '[.[] | select(.status.state == "active") | select(any(.matchers[]; .name == "alertname" and .value == "LabSLOFastBurn")) | select(.comment != "")] | length')
[ "${s:-0}" -gt 0 ] || fail "LabSLOFastBurn fired, but nobody acknowledged it: create a silence for alertname=LabSLOFastBurn with a comment"
ok "LabSLOFastBurn fired in Alertmanager and is silenced"
