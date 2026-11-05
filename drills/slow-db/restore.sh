#!/usr/bin/env bash
# Deletes any netem rule on every Postgres container of sample-db, on
# whichever node it runs, checks none is left, then waits until sample-db
# answers quickly. Never leaves a netem rule behind.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
left=0
for n in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
  kind_node_running "$n" || continue
  if pod_netns "$n" sample-db postgres tc qdisc show dev eth0 2>/dev/null | grep -q netem; then
    echo "deleting the netem rule on Postgres on $n"
    pod_netns "$n" sample-db postgres tc qdisc del dev eth0 root || true
    if pod_netns "$n" sample-db postgres tc qdisc show dev eth0 2>/dev/null | grep -q netem; then
      left=1
    fi
  fi
done
[ "$left" = 0 ] || fail "a netem rule is still on Postgres; lab drills abort tries again"
fast() {
  local start
  start=$(date +%s%N)
  kubectl get --raw /api/v1/namespaces/sample-db/services/sample-db:8080/proxy/api/check --request-timeout=5s >/dev/null 2>&1 &&
    [ $((($(date +%s%N) - start) / 1000000)) -lt 250 ]
}
wait_for 120 "sample-db to answer quickly" fast
echo "no netem rule on Postgres; sample-db answers quickly"
