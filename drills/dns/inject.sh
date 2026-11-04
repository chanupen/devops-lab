#!/usr/bin/env bash
# Scales CoreDNS to zero the way kube-downscaler does when kube-system is
# not on its exclude list: it records the replicas in an annotation and
# sets spec.replicas to 0, as its own field manager. lab keeps working: it
# talks to the API server, which does not need cluster DNS.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
replicas=$(kubectl -n kube-system get deployment coredns -o jsonpath='{.spec.replicas}')
echo "scaling CoreDNS from $replicas to 0 (as kube-downscaler would)"
kubectl -n kube-system patch deployment coredns --field-manager=kube-downscaler --type merge -p "$(
  jq -cn --arg r "$replicas" '{metadata: {annotations: {"downscaler/original-replicas": $r}}, spec: {replicas: 0}}'
)" >/dev/null
gone() { [ -z "$(kubectl -n kube-system get pods -l k8s-app=kube-dns -o name)" ]; }
wait_for 120 "the CoreDNS pods to stop" gone
echo "CoreDNS is at zero"
