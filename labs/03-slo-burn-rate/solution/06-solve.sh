#!/usr/bin/env bash
# Step 6: wait until LabSLOFastBurn reaches Alertmanager, then acknowledge
# it with a silence (POSTed through the API server's service proxy).
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
for _ in $(seq 60); do
  am_alerts | jq -e 'any(.[]; .labels.alertname == "LabSLOFastBurn")' >/dev/null && break
  sleep 10
done
am_alerts | jq -e 'any(.[]; .labels.alertname == "LabSLOFastBurn")' >/dev/null || { echo "LabSLOFastBurn did not fire in 10 minutes"; exit 1; }
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
end=$(date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+1H +%Y-%m-%dT%H:%M:%SZ)
# Alertmanager takes JSON only, so POST with curl through a port-forward
# (kubectl create --raw sends application/octet-stream). Port 0: any free one.
log=$(mktemp)
kubectl -n monitoring port-forward svc/kps-alertmanager :9093 >"$log" 2>&1 &
pf=$!
trap 'kill $pf 2>/dev/null; rm -f "$log"' EXIT
for _ in $(seq 50); do port=$(sed -n 's/^Forwarding from 127.0.0.1:\([0-9]*\).*/\1/p' "$log" | head -1); [ -n "$port" ] && break; sleep 0.2; done
[ -n "$port" ] || { echo "port-forward to Alertmanager did not start"; cat "$log"; exit 1; }
jq -n --arg s "$now" --arg e "$end" '{matchers: [{name: "alertname", value: "LabSLOFastBurn", isRegex: false, isEqual: true}],
  startsAt: $s, endsAt: $e, createdBy: "lab03", comment: "lab 3: acknowledged; the errors are injected on purpose"}' |
  curl -fsS -H 'Content-Type: application/json' -d @- "http://127.0.0.1:$port/api/v2/silences"
echo
