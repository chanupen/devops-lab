#!/usr/bin/env bash
# Recovered: podinfo-backend keeps the release (its in-memory catalogue)
# and runs within a right-sized memory limit: requests of at least 128Mi
# (what it uses), a limit of 160Mi to 1Gi (headroom over the ~140Mi peak,
# but still a cap), set in Argo CD's desired state (the app is Synced, so
# self-heal will not undo it) and with no OOM kill since.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
mi() { # a Kubernetes memory quantity in MiB, or -1
  local q=$1
  case "$q" in
  *Gi) echo $((${q%Gi} * 1024)) ;;
  *Mi) echo "${q%Mi}" ;;
  *Ki) echo $((${q%Ki} / 1024)) ;;
  *G) echo $((${q%G} * 1000 * 1000 * 1000 / 1048576)) ;;
  *M) echo $((${q%M} * 1000 * 1000 / 1048576)) ;;
  '' | *[!0-9]*) echo -1 ;;
  *) echo $((q / 1048576)) ;;
  esac
}
# The symptom first: until the backend runs steadily, say only that.
pods=$(kubectl -n podinfo get pods -l app.kubernetes.io/name=podinfo-backend -o json)
bad=$(jq -r '[.items[] | select(.metadata.deletionTimestamp == null) | .status.containerStatuses[]? |
  select((.ready | not) or .restartCount > 0)] | length' <<<"$pods")
[ "$bad" = 0 ] || fail "the backend still does not run steadily: a pod is not ready or keeps restarting"
rolled_out podinfo podinfo-backend || fail "the backend still does not run steadily: its pods are not all up yet"
d=$(kubectl -n podinfo get deployment podinfo-backend -o json) || fail "no Deployment podinfo-backend"
c=$(jq '.spec.template.spec.containers[0]' <<<"$d")
[[ "$(jq -r '.command | join(" ")' <<<"$c")" == *--stress-memory=128* ]] ||
  fail "the release is gone (no --stress-memory=128): the drill asks you to keep it and right-size its memory, not to roll it back"
limit=$(mi "$(jq -r '.resources.limits.memory // ""' <<<"$c")")
request=$(mi "$(jq -r '.resources.requests.memory // ""' <<<"$c")")
[ "$limit" -gt 0 ] || fail "podinfo-backend has no memory limit: keep a cap, so a leak cannot take the node's memory"
[ "$limit" -ge 160 ] || fail "the memory limit is ${limit}Mi; the app needs about 140Mi at its peak, so leave headroom (160Mi or more)"
[ "$limit" -le 1024 ] || fail "the memory limit is ${limit}Mi, far more than the app needs: right-size it (160Mi to 1Gi)"
[ "$request" -ge 128 ] || fail "the memory request is ${request}Mi: the scheduler reserves only that, so ask for what the app uses (128Mi or more)"
[ "$request" -le "$limit" ] || fail "the request (${request}Mi) is above the limit (${limit}Mi)"
[ "$(app_status podinfo-backend)" = Synced/Healthy ] ||
  fail "Application podinfo-backend is $(app_status podinfo-backend): set the memory in Argo CD's desired state (argocd app set), so self-heal keeps it"
ok "podinfo-backend keeps its catalogue with requests ${request}Mi and limit ${limit}Mi, set through Argo CD"
