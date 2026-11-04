#!/usr/bin/env bash
# Step 2: start the traffic Job and wait for its first progress line.
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
set -e
kubectl -n "$TNS" delete job traffic --ignore-not-found --wait
kubectl apply -f "$(dirname "$0")/../app/traffic.yaml"
for _ in $(seq 60); do
  [ -n "$(traffic_progress)" ] && exit 0
  sleep 2
done
echo "no progress from the traffic Job after 2 minutes" >&2
exit 1
