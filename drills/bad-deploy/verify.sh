#!/usr/bin/env bash
# Recovered: podinfo-frontend runs a good release again, every replica is
# ready and answers, and the running release came from Argo CD (its last
# sync is not the bad release), not from kubectl.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
bad=podinfo:6.15.1
image=$(kubectl -n podinfo get deployment podinfo-frontend -o jsonpath='{.spec.template.spec.containers[0].image}')
last=$(kubectl -n argocd get application podinfo-frontend -o json |
  jq -r '.status.history[-1].source.helm.parameters // [] | map(.value) | join(" ")')
case "$last" in
*6.15.1*)
  case "$image" in
  *"$bad") fail "podinfo-frontend still runs $image: find out where it came from (argocd app history podinfo-frontend)" ;;
  *) fail "the Deployment runs $image, but Argo CD's last sync is still the bad release: it was changed with kubectl, so self-heal or the next sync puts it back. Roll back through Argo CD" ;;
  esac
  ;;
esac
case "$image" in *"$bad") fail "podinfo-frontend still runs $image" ;; esac
rolled_out podinfo podinfo-frontend || fail "podinfo-frontend is not rolled out yet: kubectl -n podinfo rollout status deployment podinfo-frontend"
crash=$(kubectl -n podinfo get pods -l app.kubernetes.io/name=podinfo-frontend -o json |
  jq -r '[.items[] | select(.metadata.deletionTimestamp == null) | .status.containerStatuses[]? | select(.ready | not)] | length')
[ "$crash" = 0 ] || fail "$crash podinfo-frontend container(s) are not ready"
kubectl get --raw /api/v1/namespaces/podinfo/services/podinfo-frontend:9898/proxy/readyz >/dev/null 2>&1 ||
  fail "podinfo-frontend does not answer on /readyz"
ok "podinfo-frontend runs $image again, rolled back through Argo CD"
