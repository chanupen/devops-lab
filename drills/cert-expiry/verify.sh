#!/usr/bin/env bash
# Recovered: sample-db's certificate is renewed (its renewal time is not
# overdue) and Ready, the CA Issuer is Ready with the CA Secret cert-manager
# manages, and the fix came through Argo CD (its last sync is not the bad
# release). Until the certificate renews, or while the CA Secret the Issuer
# names does not exist, the message describes the symptom only.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
ns=sample-db
c=$(kubectl -n $ns get certificate sample-db-tls -o json) || fail "no Certificate sample-db-tls in $ns"
renew=$(jq -r '.status.renewalTime // ""' <<<"$c")
expires=$(jq -r '.status.notAfter // ""' <<<"$c")
if [ -z "$renew" ] || [ "$(date -d "$renew" +%s)" -lt $(($(date +%s) - 60)) ]; then
  fail "sample-db's certificate is still not renewed: it expires at $(date -d "$expires" +%H:%M 2>/dev/null || echo "$expires")"
fi
[ "$(jq -r '.status.conditions[]? | select(.type == "Ready") | .status' <<<"$c")" = True ] || fail "sample-db's certificate is not Ready"
i=$(kubectl -n $ns get issuer sample-db-ca -o json) || fail "no Issuer sample-db-ca"
ca=$(jq -r '.spec.ca.secretName' <<<"$i")
if [ "$ca" != sample-db-ca ]; then
  # Name the Secret only once it exists: then the learner has found it and
  # created it by hand. Before that the renewal is still ahead: symptom only.
  kubectl -n $ns get secret "$ca" >/dev/null 2>&1 ||
    fail "sample-db's certificate is due for renewal at $(date -d "$renew" +%H:%M) and is not renewed yet: it expires at $(date -d "$expires" +%H:%M 2>/dev/null || echo "$expires")"
  fail "the certificate renews, but its Issuer signs with $ca, a Secret cert-manager does not manage: fix the release instead"
fi
[ "$(jq -r '.status.conditions[]? | select(.type == "Ready") | .status' <<<"$i")" = True ] || fail "the Issuer sample-db-ca is not Ready"
last=$(kubectl -n argocd get application sample-db -o json | jq -r '.status.history[-1].source.helm.parameters // [] | map(.name + "=" + .value) | join(" ")')
case "$last" in
*sample-db-ca-2026*) fail "the Issuer is right, but Argo CD's last sync is still the release that broke it: it was changed with kubectl. Fix it through Argo CD" ;;
esac
ok "sample-db's certificate renews again (next at $(date -d "$renew" +%H:%M)), from the CA cert-manager manages"
