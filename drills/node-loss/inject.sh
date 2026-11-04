#!/usr/bin/env bash
# Stops the kind worker node that runs podinfo-frontend, as a crashed host
# would. It prefers a worker without Prometheus or Alertmanager, so the
# alert can fire while the node is gone. The node's name goes to the run's
# directory for restore.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
workers=$(kind_workers)
[ "$(wc -w <<<"$workers")" -ge 2 ] || fail "node-loss needs two kind workers; this lab has: ${workers:-none}"
for w in $workers; do
  node_ready "$w" || fail "worker $w is not Ready already: lab drills needs a healthy lab (lab status)"
done
on() { kubectl get pods -A --field-selector "spec.nodeName=$1" -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}'; }
frontend=$(kubectl -n podinfo get pods -l app.kubernetes.io/name=podinfo-frontend -o jsonpath='{.items[0].spec.nodeName}')
backend=$(kubectl -n podinfo get pods -l app.kubernetes.io/name=podinfo-backend -o jsonpath='{.items[0].spec.nodeName}')
pick=""
for n in $frontend $backend; do
  if ! on "$n" | grep -qE '^monitoring/(prometheus-kps-prometheus|alertmanager-kps-alertmanager)-'; then
    pick=$n
    break
  fi
done
if [ -z "$pick" ]; then
  pick=$frontend
  echo "note: $pick also runs Prometheus or Alertmanager, so the alert comes late"
fi
grep -qw "$pick" <<<"$workers" || fail "podinfo-frontend runs on $pick, not on a worker"
mkdir -p "$DRILL_STATE"
echo "$pick" >"$DRILL_STATE/node"
echo "stopping kind node $pick (it runs: $(on "$pick" | grep -c .) pods)"
podman stop -t 10 "$pick" >/dev/null
echo "$pick is stopped"
