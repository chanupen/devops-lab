#!/usr/bin/env bash
# Back to the start of lab 3: no lab03-* namespaces (the rules, the load and
# the error Job go with them), and no silence the lab created. Alerts that
# fired resolve on their own once the rules are gone.
set -uo pipefail
# shellcheck source=SCRIPTDIR/steps/lib.sh
. "$(dirname "$0")/steps/lib.sh"
for id in $(kubectl get --raw "$AM/api/v2/silences" --request-timeout=15s 2>/dev/null |
  jq -r '.[] | select(.status.state != "expired") | select(any(.matchers[]; .name == "alertname" and (.value | startswith("LabSLO")))) | .id'); do
  kubectl delete --raw "$AM/api/v2/silence/$id" >/dev/null 2>&1
done
kubectl delete namespace lab03-slo lab03-load --ignore-not-found --wait --timeout=180s
echo "lab 3 is back at its start"
