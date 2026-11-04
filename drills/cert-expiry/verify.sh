#!/usr/bin/env bash
# Recovered: sample-db's certificate is renewed (its renewal time is not
# overdue) and Ready, the CA Issuer is Ready with the CA Secret cert-manager
# manages, and the fix came through Argo CD (its last sync is not the bad
# release). Until the certificate renews, the message describes the
# symptom only.
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
[ "$(jq -r '.spec.ca.secretName' <<<"$i")" = sample-db-ca ] ||
  fail "the certificate renews, but its Issuer signs with $(jq -r .spec.ca.secretName <<<"$i"), a Secret cert-manager does not manage: fix the release instead"
[ "$(jq -r '.status.conditions[]? | select(.type == "Ready") | .status' <<<"$i")" = True ] || fail "the Issuer sample-db-ca is not Ready"
last=$(kubectl -n argocd get application sample-db -o json | jq -r '.status.history[-1].source.helm.parameters // [] | map(.name + "=" + .value) | join(" ")')
case "$last" in
*sample-db-ca-2026*) fail "the Issuer is right, but Argo CD's last sync is still the release that broke it: it was changed with kubectl. Fix it through Argo CD" ;;
esac
ok "sample-db's certificate renews again (next at $(date -d "$renew" +%H:%M)), from the CA cert-manager manages"
