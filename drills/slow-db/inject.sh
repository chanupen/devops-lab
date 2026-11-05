#!/usr/bin/env bash
# Adds 300 ms (±50 ms) of delay to everything Postgres sends, with tc netem
# in the Postgres container's network namespace, run from the kind node
# (see the README for why not Chaos Mesh).
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
node=$(kubectl -n sample-db get pod postgres-0 -o jsonpath='{.spec.nodeName}') || fail "no pod postgres-0 in sample-db: is the sample-db stack up?"
[ -n "$node" ] || fail "postgres-0 is not scheduled"
pod_netns "$node" sample-db postgres tc qdisc show dev eth0 | grep -q netem && fail "Postgres already has a netem rule"
mkdir -p "$DRILL_STATE"
echo "$node" >"$DRILL_STATE/node"
pod_netns "$node" sample-db postgres tc qdisc add dev eth0 root netem delay 300ms 50ms
echo "Postgres (postgres-0 on $node) now sends with 300ms ±50ms of delay"
