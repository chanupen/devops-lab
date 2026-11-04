#!/usr/bin/env bash
# lab has put CoreDNS's Deployment back as it was (replicas, annotations).
# Wait until it serves again, then until Argo CD, which lost DNS too (it
# could not reach Git), has every app Synced and Healthy again.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
wait_for 120 "CoreDNS to roll out" rolled_out kube-system coredns
kubectl -n argocd annotate application lab-bootstrap argocd.argoproj.io/refresh=normal --overwrite >/dev/null
wait_for 300 "every Argo CD app to be Synced and Healthy" all_apps_ready
echo "CoreDNS is back"
