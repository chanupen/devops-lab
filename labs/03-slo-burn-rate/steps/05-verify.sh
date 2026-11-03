#!/usr/bin/env bash
# Step 5: errors are injected: over 2 minutes more than 7.2% of requests
# fail, a burn rate above 14.4.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_prometheus
v=$(query 'lab03:sli_errors:ratio_rate2m')
[ -n "$v" ] || fail "lab03:sli_errors:ratio_rate2m has no value: check steps 1 and 2"
gt "$v" 0.072 || fail "only $(awk -v v="$v" 'BEGIN { printf "%.1f", v * 100 }')% of requests fail over 2m; the Job in lab03-load must make more than 7.2% fail"
ok "$(awk -v v="$v" 'BEGIN { printf "%.1f", v * 100 }')% of requests fail over 2m: the error budget burns at $(awk -v v="$v" 'BEGIN { printf "%.0f", v / 0.005 }')x"
