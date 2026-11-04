#!/usr/bin/env bash
# The reference fix: right-size the memory in the desired state. The
# release needs about 140Mi; request what it uses and cap it with
# headroom. Argo CD rolls it out; kubectl edit would be undone by
# self-heal. (In a repo you own, this is a commit to the values file.)
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
argocd_core app set podinfo-backend -p resources.requests.memory=160Mi -p resources.limits.memory=256Mi
argocd_core app wait podinfo-backend --health --sync --timeout 180
kubectl -n podinfo rollout status deployment podinfo-backend --timeout=180s
