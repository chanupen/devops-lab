#!/usr/bin/env bash
# Two releases of sample-db through Argo CD (Helm parameters, not Git):
# one-hour certificates (issued at once), then a CA "rotation" that points
# the CA Issuer at a Secret that does not exist. Renewal, due two minutes
# after issuance, then cannot happen.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
ns=sample-db
kubectl -n $ns get certificate sample-db-tls >/dev/null 2>&1 || fail "no Certificate sample-db-tls: is sample-db up with tls.enabled?"
mkdir -p "$DRILL_STATE"
kubectl -n $ns get certificaterequest -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' >"$DRILL_STATE/requests"
good_release sample-db
short=(tls.duration=1h tls.renewBefore=58m)
echo "release 1: one-hour certificates"
release sample-db "${short[@]}"
# The API server keeps the duration as written: 1h from the parameter.
duration() { case "$(kubectl -n $ns get certificate sample-db-tls -o jsonpath='{.spec.duration}')" in 1h | 1h0m0s) ;; *) return 1 ;; esac; }
wait_for 180 "Argo CD to apply the one-hour lifetime" duration
# What cmctl renew does: mark the certificate for issuance now.
kubectl -n $ns patch certificate sample-db-tls --subresource=status --type json -p "$(
  jq -cn --arg t "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '[{op: "add", path: "/status/conditions/-", value: {type: "Issuing", status: "True",
    reason: "ManuallyTriggered", message: "Certificate re-issuance manually triggered", lastTransitionTime: $t}}]'
)" >/dev/null
short_lived() {
  local na
  na=$(kubectl -n $ns get secret sample-db-tls -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -enddate | cut -d= -f2)
  [ -n "$na" ] && [ "$(date -d "$na" +%s)" -lt $(($(date +%s) + 7200)) ] &&
    [ "$(kubectl -n $ns get certificate sample-db-tls -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = True ]
}
wait_for 180 "a one-hour certificate" short_lived
echo "release 2: a CA rotation that names a CA Secret nobody created"
release sample-db "${short[@]}" tls.caSecretName=sample-db-ca-2026
broken() { [ "$(kubectl -n $ns get issuer sample-db-ca -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = False ]; }
wait_for 180 "the CA Issuer to lose its CA" broken
echo "the CA Issuer cannot sign; renewal is due in about two minutes"
