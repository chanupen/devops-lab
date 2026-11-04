#!/usr/bin/env bash
# The reference fix: follow the certificate to its pending request and the
# Issuer, find the release that changed the Issuer in Argo CD's history,
# and remove that parameter through Argo CD. The pending renewal goes
# through on its own.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
kubectl -n sample-db get certificate,certificaterequest,issuer
kubectl -n sample-db get issuer sample-db-ca -o jsonpath='{.status.conditions[0].message}{"\n"}'
argocd_core app history sample-db
argocd_core app unset sample-db -p tls.caSecretName
renewed() {
  local r
  r=$(kubectl -n sample-db get certificate sample-db-tls -o jsonpath='{.status.renewalTime}')
  [ -n "$r" ] && [ "$(date -d "$r" +%s)" -gt "$(date +%s)" ]
}
wait_for 180 "the certificate to renew" renewed
kubectl -n sample-db get certificate sample-db-tls
