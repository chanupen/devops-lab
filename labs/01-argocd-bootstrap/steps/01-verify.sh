#!/usr/bin/env bash
# Step 1: Argo CD is installed with Helm, chart argo-cd 10.10.5, in
# namespace argocd, and every pod there is Ready.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
want=argo-cd-10.10.5
need_cluster
releases=$(helm list -n argocd -o json 2>/dev/null) || fail "helm cannot list releases in namespace argocd"
chart=$(jq -r '[.[] | select(.chart | startswith("argo-cd-"))][0].chart // empty' <<<"$releases")
[ -n "$chart" ] || fail "no Helm release of the argo-cd chart in namespace argocd"
[ "$chart" = "$want" ] || fail "the release uses $chart; this lab pins $want (--version 10.10.5)"
status=$(jq -r '[.[] | select(.chart == "'"$want"'")][0].status' <<<"$releases")
[ "$status" = deployed ] || fail "the Helm release is $status, not deployed"
pods=$(kubectl -n argocd get pods -o json) || fail "cannot list pods in namespace argocd"
total=$(jq '[.items[] | select(.status.phase != "Succeeded")] | length' <<<"$pods")
[ "$total" -gt 0 ] || fail "no pods in namespace argocd yet"
not_ready=$(jq -r '[.items[] | select(.status.phase != "Succeeded")
  | select(([.status.conditions[]? | select(.type == "Ready" and .status == "True")] | length) == 0) | .metadata.name] | join(", ")' <<<"$pods")
[ -z "$not_ready" ] || fail "not Ready yet: $not_ready"
ok "Argo CD is installed ($want) and its $total pods are Ready"
