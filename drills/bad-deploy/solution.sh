#!/usr/bin/env bash
# The reference fix: an emergency rollback through Argo CD. Auto-sync is
# paused first (Argo CD refuses to roll back while it is on), then the app
# goes back to its last release before the bad one. The bad release stays
# in the history; lab drills verify ends the drill and lab puts auto-sync
# and Git's desired state back.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
argocd_core app history podinfo-frontend
good=$(kubectl -n argocd get application podinfo-frontend -o json |
  jq -r '[.status.history[] | select(((.source.helm.parameters // []) | map(.value) | join(" ")) | contains("6.15.1") | not)] | last | .id')
[ -n "$good" ] && [ "$good" != null ] || fail "no good release in the history"
argocd_core app set podinfo-frontend --sync-policy none
argocd_core app rollback podinfo-frontend "$good" --timeout 180
kubectl -n podinfo rollout status deployment podinfo-frontend --timeout=180s
