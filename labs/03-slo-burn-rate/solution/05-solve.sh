#!/usr/bin/env bash
# Step 5: start the error Job, then wait until the error ratio shows it.
set -euo pipefail
kubectl apply -f "$(dirname "$0")/05-errors.yaml"
kubectl -n lab03-load wait --for=jsonpath='{.status.active}'=1 job/errors --timeout=120s
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
for _ in $(seq 40); do
  v=$(query 'lab03:sli_errors:ratio_rate2m')
  [ -n "$v" ] && gt "$v" 0.072 && exit 0
  sleep 6
done
echo "the error ratio did not rise above 7.2% within 4 minutes"
exit 1
