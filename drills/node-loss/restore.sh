#!/usr/bin/env bash
# Starts every stopped kind node of the lab (the one the run stopped, and
# any other), waits until all nodes are Ready, then until Argo CD has
# every app Synced and Healthy and the podinfo apps are rolled out. Never
# leaves a node stopped.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
nodes=$(kubectl get nodes -o jsonpath='{.items[*].metadata.name}')
[ -s "$DRILL_STATE/node" ] && nodes="$nodes $(cat "$DRILL_STATE/node")"
for n in $(tr ' ' '\n' <<<"$nodes" | sort -u); do
  if ! kind_node_running "$n"; then
    echo "starting kind node $n"
    podman start "$n" >/dev/null || fail "podman start $n failed"
  fi
done
for n in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
  wait_for 300 "node $n to be Ready" node_ready "$n"
done
kubectl -n argocd annotate application lab-bootstrap argocd.argoproj.io/refresh=normal --overwrite >/dev/null
wait_for 300 "every Argo CD app to be Synced and Healthy" all_apps_ready
for d in podinfo-frontend podinfo-backend; do
  wait_for 300 "$d to roll out" rolled_out podinfo "$d"
done
gone() { [ "$(kubectl -n podinfo get pods -o json | jq '[.items[] | select(.metadata.deletionTimestamp != null)] | length')" = 0 ]; }
wait_for 180 "the podinfo pods on the stopped node to finish terminating" gone
echo "every node is Ready and the podinfo apps serve"
