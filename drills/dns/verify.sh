#!/usr/bin/env bash
# Recovered: the frontend reaches the backend by name again, and CoreDNS
# runs with the replicas it had (two in kind), all ready. Until names
# resolve again, the message describes the symptom only.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
# The frontend calls the backend by its Service name (podinfo-backend.podinfo).
# It answers 200 either way and puts a failed call in the body.
out=$(echo '{"drill":"dns"}' | kubectl create --raw /api/v1/namespaces/podinfo/services/podinfo-frontend:9898/proxy/api/echo -f - 2>&1) ||
  fail "the frontend does not answer"
if grep -q 'call failed' <<<"$out" || ! grep -q 'drill' <<<"$out"; then
  fail "the frontend still can't reach the backend by name"
fi
d=$(kubectl -n kube-system get deployment coredns -o json) || fail "no Deployment coredns in kube-system"
want=$(jq -r '.metadata.annotations["downscaler/original-replicas"] // "2"' <<<"$d")
replicas=$(jq -r '.spec.replicas' <<<"$d")
ready=$(jq -r '.status.readyReplicas // 0' <<<"$d")
[ "$replicas" -ge "$want" ] || fail "names resolve, but CoreDNS has $replicas replicas; it had $want"
[ "$ready" -ge "$replicas" ] || fail "names resolve, but CoreDNS has $ready of $replicas replicas ready"
ok "CoreDNS runs $ready/$replicas and the frontend reaches the backend by name"
