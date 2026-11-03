#!/usr/bin/env bash
# Step 3: the SLO is recorded (99.5% over 30 days), and the burn rate over
# each window is the error ratio divided by the error budget.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_prometheus
o=$(query 'lab03:slo:objective_ratio')
[ -n "$o" ] || fail "no recording rule lab03:slo:objective_ratio (or no value yet)"
[ "$(awk -v o="$o" 'BEGIN { print (o == 0.995) }')" = 1 ] || fail "lab03:slo:objective_ratio is $o, want 0.995 (99.5%)"
win=$(rule lab03:slo:objective_ratio | jq -r '.labels.slo_window // ""')
[ "$win" = 30d ] || fail "lab03:slo:objective_ratio needs the label slo_window=\"30d\", it has '$win'"
for w in 2m 10m 1h 12h; do
  r=$(rule "lab03:slo:burn_rate$w")
  [ -n "$r" ] || fail "no recording rule lab03:slo:burn_rate$w"
  grep -q "lab03:sli_errors:ratio_rate$w" <<<"$(jq -r .query <<<"$r")" || fail "lab03:slo:burn_rate$w does not use lab03:sli_errors:ratio_rate$w"
done
b=$(query 'lab03:slo:burn_rate2m')
[ -n "$b" ] || fail "lab03:slo:burn_rate2m has no value yet"
ok "SLO 99.5% over 30d; burn rate over 2m is now $(printf '%.2f' "$b")"
