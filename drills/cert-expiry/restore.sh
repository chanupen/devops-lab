#!/usr/bin/env bash
# lab has put the Applications back. Wait until Argo CD has synced Git's
# release, the CA Issuer is Ready and the certificate is Ready with Git's
# lifetime (cert-manager reissues a short-lived one at once: its renewal
# time, notAfter minus 30 days, is past), then delete the
# CertificateRequests the drill made.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
ns=sample-db
wait_lab_back sample-db
issuer_ready() { [ "$(kubectl -n $ns get issuer sample-db-ca -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = True ]; }
wait_for 180 "the CA Issuer to be Ready" issuer_ready
long_lived() {
  local na
  [ "$(kubectl -n $ns get certificate sample-db-tls -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = True ] || return 1
  na=$(kubectl -n $ns get secret sample-db-tls -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -enddate | cut -d= -f2)
  [ -n "$na" ] && [ "$(date -d "$na" +%s)" -gt $(($(date +%s) + 86400 * 31)) ]
}
wait_for 300 "a certificate with Git's lifetime" long_lived
if [ -f "$DRILL_STATE/requests" ]; then
  for r in $(kubectl -n $ns get certificaterequest -o jsonpath='{.items[*].metadata.name}'); do
    grep -qx "$r" "$DRILL_STATE/requests" || kubectl -n $ns delete certificaterequest "$r" --wait=false >/dev/null
  done
fi
echo "the Issuer and sample-db's certificate are back as Git has them"
