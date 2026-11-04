#!/usr/bin/env bash
# Step 5: force ESO to sync now, then wait for the kubelet to update the
# file and the app to move to app_v2.
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
set -e
kubectl -n "$NS" annotate externalsecret db-app --overwrite "force-sync=$(date +%s)"
for _ in $(seq 90); do
  [ "$(api /api/check | jq -r '.user // empty' 2>/dev/null)" = app_v2 ] && exit 0
  sleep 2
done
echo "the app did not move to app_v2 within 3 minutes" >&2
exit 1
