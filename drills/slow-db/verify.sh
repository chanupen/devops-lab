#!/usr/bin/env bash
# Recovered: no netem rule on any Postgres container, and /api/check is
# fast again. Until requests are fast, the message describes the symptom
# only.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
slow=0
for _ in 1 2 3 4 5; do
  start=$(date +%s%N)
  kubectl get --raw /api/v1/namespaces/sample-db/services/sample-db:8080/proxy/api/check --request-timeout=10s >/dev/null 2>&1 ||
    fail "sample-db does not answer /api/check"
  ms=$((($(date +%s%N) - start) / 1000000))
  [ "$ms" -gt 250 ] && slow=$((slow + 1))
done
[ "$slow" -le 1 ] || fail "requests to sample-db are still slow: $slow of 5 took over 250 ms"
for n in $(kubectl get pods -n sample-db -l app=postgres -o jsonpath='{.items[*].spec.nodeName}'); do
  if pod_netns "$n" sample-db postgres tc qdisc show dev eth0 2>/dev/null | grep -q netem; then
    fail "requests are fast again, but a netem rule is still on Postgres's interface"
  fi
done
ok "sample-db answers within 250 ms again and Postgres's interface has no netem rule"
