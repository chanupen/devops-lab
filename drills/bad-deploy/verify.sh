#!/usr/bin/env bash
# Recovered: podinfo-frontend runs a good release again, every replica is
# ready and answers, and the running release came from Argo CD (its last
# sync is not the bad release), not from kubectl. Until the frontend
# serves again, the message describes the symptom only.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
bad=podinfo:6.15.1
notready=$(kubectl -n podinfo get pods -l app.kubernetes.io/name=podinfo-frontend -o json |
  jq -r '[.items[] | select(.metadata.deletionTimestamp == null) | .status.containerStatuses[]? | select(.ready | not)] | length')
[ "$notready" = 0 ] || fail "the frontend still does not serve: $notready of its pods are not ready"
rolled_out podinfo podinfo-frontend || fail "the frontend still does not serve: its pods are not all up yet"
kubectl get --raw /api/v1/namespaces/podinfo/services/podinfo-frontend:9898/proxy/readyz >/dev/null 2>&1 ||
  fail "the frontend still does not answer"
image=$(kubectl -n podinfo get deployment podinfo-frontend -o jsonpath='{.spec.template.spec.containers[0].image}')
last=$(kubectl -n argocd get application podinfo-frontend -o json |
  jq -r '.status.history[-1].source.helm.parameters // [] | map(.value) | join(" ")')
case "$last" in
*6.15.1*)
  case "$image" in
  *"$bad") fail "the frontend answers, but still runs $image" ;;
  *) fail "the Deployment runs $image, but Argo CD's last sync is still the bad release: it was changed with kubectl, so self-heal or the next sync puts it back. Roll back through Argo CD" ;;
  esac
  ;;
esac
case "$image" in *"$bad") fail "the frontend answers, but still runs $image" ;; esac
ok "podinfo-frontend runs $image again, rolled back through Argo CD"
