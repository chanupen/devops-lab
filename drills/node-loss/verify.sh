#!/usr/bin/env bash
# Recovered: every node is Ready (the lost one is back), the podinfo apps
# are rolled out with every pod ready on a Ready node, and the frontend
# reaches the backend. Until the app serves again, the message describes
# the symptom only.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
for d in podinfo-frontend podinfo-backend; do
  rolled_out podinfo "$d" || fail "users still get errors: the app is not fully serving"
done
out=$(echo '{"drill":"node-loss"}' | kubectl create --raw /api/v1/namespaces/podinfo/services/podinfo-frontend:9898/proxy/api/echo -f - 2>&1) ||
  fail "users still get errors: the frontend does not answer"
grep -q 'call failed' <<<"$out" && fail "users still get errors: the frontend cannot reach the backend"
notready=$(kubectl get nodes -o json | jq -r '[.items[] | select(any(.status.conditions[]; .type == "Ready" and .status != "True")) | .metadata.name] | join(" ")')
[ -z "$notready" ] || fail "the app serves again, but node $notready is still NotReady: bring it back"
for w in $(kind_workers); do
  kind_node_running "$w" || fail "the app serves again, but kind node $w is not running"
done
stuck=$(kubectl -n podinfo get pods -o json | jq -r '[.items[] | select(.metadata.deletionTimestamp != null)] | length')
[ "$stuck" = 0 ] || fail "$stuck podinfo pod(s) are still Terminating"
ok "every node is Ready and the podinfo apps serve again"
