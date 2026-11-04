#!/usr/bin/env bash
# The reference fix: confirm the database is fast locally, find the netem
# rule on Postgres's interface with a NET_ADMIN debug container, delete it.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
netshoot=docker.io/nicolaka/netshoot:v0.15@sha256:47b907d662d139d1e2f22bfe14f4efca1e3f1feed283572f47c970c780c03b61
kubectl -n sample-db exec postgres-0 -- psql -U postgres -d app -c '\timing' -c 'select current_user'
debug() {
  kubectl -n sample-db debug postgres-0 --profile=netadmin --target=postgres --image="$netshoot" --attach --quiet -- "$@"
}
debug tc qdisc show dev eth0
debug tc qdisc del dev eth0 root
debug tc qdisc show dev eth0
