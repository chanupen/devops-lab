#!/usr/bin/env bash
# Step 1: steady traffic reaches podinfo-frontend, so the SLI has requests
# to count: more than 1 request a second (probes alone are about 0.2).
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_prometheus
r=$(query "sum(rate(${TOTAL}[2m]))")
[ -n "$r" ] || fail "Prometheus has no http_requests_total for podinfo-frontend: is the sample-app stack up?"
gt "$r" 1 || fail "podinfo-frontend gets $(printf '%.2f' "$r") requests/s; run the load Deployment in lab03-load (more than 1/s)"
ok "podinfo-frontend gets $(printf '%.1f' "$r") requests/s"
