#!/usr/bin/env bash
# Step 2: the traffic Job runs, reports progress, and has seen no failure.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
active=$(kubectl -n "$TNS" get job traffic -o jsonpath='{.status.active}' 2>/dev/null)
[ "${active:-0}" -ge 1 ] || fail "Job traffic in $TNS is not running: kubectl apply -f app/traffic.yaml (delete a finished one first)"
line=$(traffic_progress)
[ -n "$line" ] || fail "the traffic Job has not reported progress yet (every 10 seconds)"
failed=$(sed -n 's/.*failed=\([0-9]*\).*/\1/p' <<<"$line")
okn=$(sed -n 's/.*ok=\([0-9]*\).*/\1/p' <<<"$line")
[ "$failed" = 0 ] || fail "the traffic Job saw $failed failed requests before the rotation: fix the app, then delete and re-create the Job"
[ "${okn:-0}" -gt 0 ] || fail "the traffic Job has no successful request yet"
ok "traffic runs: $okn requests, 0 failed"
