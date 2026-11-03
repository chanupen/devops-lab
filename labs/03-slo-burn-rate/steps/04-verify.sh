#!/usr/bin/env bash
# Step 4: the workbook's three alerts are loaded, each with a long and a
# short window: fast burn 14.4 and slow burn 6 page, budget burn 1 is a
# ticket.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_prometheus
check() { # name threshold long short severity
  local r q
  r=$(rule "$1")
  [ -n "$r" ] || fail "no alert $1 from a PrometheusRule in lab03-slo"
  [ "$(jq -r .type <<<"$r")" = alerting ] || fail "$1 is a recording rule, want an alert"
  q=$(jq -r .query <<<"$r")
  if ! grep -q "lab03:slo:burn_rate$3" <<<"$q" || ! grep -q "lab03:slo:burn_rate$4" <<<"$q"; then
    fail "$1 must check both windows, lab03:slo:burn_rate$3 and lab03:slo:burn_rate$4: $q"
  fi
  grep -qE "> *$2([^0-9.]|$)" <<<"$q" || fail "$1 must fire above a burn rate of $2: $q"
  [ "$(jq -r '.labels.severity // ""' <<<"$r")" = "$5" ] || fail "$1 needs the label severity=$5"
}
check LabSLOFastBurn 14.4 10m 2m page
check LabSLOSlowBurn 6 1h 10m page
check LabSLOBudgetTicket 1 12h 1h ticket
ok "LabSLOFastBurn (14.4, 10m/2m) and LabSLOSlowBurn (6, 1h/10m) page; LabSLOBudgetTicket (1, 12h/1h) is a ticket"
