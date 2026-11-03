# shellcheck shell=bash
# Variables here are used by the scripts that source this file.
# shellcheck disable=SC2034
# Shared by lab 3's verify scripts. They read the Prometheus and
# Alertmanager APIs only (through the API server's service proxy), never
# change anything, and exit 0 when the step is done or print why not.
set -uo pipefail

fail() {
  echo "$*"
  exit 1
}

ok() {
  echo "$*"
  exit 0
}

PROM=/api/v1/namespaces/monitoring/services/http:kps-prometheus:9090/proxy
AM=/api/v1/namespaces/monitoring/services/http:kps-alertmanager:9093/proxy

# The SLI: podinfo-frontend's requests, and those that failed with a 5xx.
TOTAL='http_requests_total{namespace="podinfo",job="podinfo-frontend"}'
ERRORS='http_requests_total{namespace="podinfo",job="podinfo-frontend",status=~"5.."}'

need_prometheus() {
  kubectl get --raw "$PROM/-/ready" --request-timeout=10s >/dev/null 2>&1 ||
    fail "Prometheus does not answer: is the obs stack up? lab status"
}

# query <promql>: the first sample's value, or "" when there is none.
query() {
  local q
  q=$(jq -rn --arg q "$1" '$q|@uri')
  kubectl get --raw "$PROM/api/v1/query?query=$q" --request-timeout=15s 2>/dev/null |
    jq -r '.data.result[0].value[1] // ""'
}

# rules: every rule Prometheus has loaded from PrometheusRules in lab03-slo.
rules() {
  kubectl get --raw "$PROM/api/v1/rules" --request-timeout=15s 2>/dev/null |
    jq -c '[.data.groups[] | select(.file | test("lab03-slo")) | .rules[]]'
}

# rule <name>: the loaded recording rule or alert with that name, or "".
rule() {
  rules | jq -c --arg n "$1" 'map(select(.name == $n)) | .[0] // empty'
}

# gt <a> <b>: a > b, as numbers.
gt() { awk -v a="$1" -v b="$2" 'BEGIN { exit !(a + 0 > b + 0) }'; }

# am_alerts: the alerts Alertmanager holds, as JSON.
am_alerts() {
  kubectl get --raw "$AM/api/v2/alerts" --request-timeout=15s 2>/dev/null
}
