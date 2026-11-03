#!/usr/bin/env bash
# Step 2: recording rules for the error ratio over 2m, 10m, 1h and 12h are loaded
# from a PrometheusRule in lab03-slo, and have values.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_prometheus
for w in 2m 10m 1h 12h; do
  r=$(rule "lab03:sli_errors:ratio_rate$w")
  [ -n "$r" ] || fail "Prometheus has no recording rule lab03:sli_errors:ratio_rate$w from a PrometheusRule in lab03-slo (a new rule takes up to a minute to load)"
  [ "$(jq -r .type <<<"$r")" = recording ] || fail "lab03:sli_errors:ratio_rate$w is an alert, want a recording rule"
  q=$(jq -r .query <<<"$r")
  grep -q "\[$w\]" <<<"$q" || fail "lab03:sli_errors:ratio_rate$w does not use a [$w] range: $q"
done
v=$(query 'lab03:sli_errors:ratio_rate2m')
[ -n "$v" ] || fail "lab03:sli_errors:ratio_rate2m has no value: while nothing fails there is no 5xx series, so add 'or vector(0)' to the numerator (or wait one evaluation, 30s)"
ok "the error ratio is recorded over 2m, 10m, 1h and 12h (now $(printf '%.4f' "$v"))"
