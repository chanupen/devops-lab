# shellcheck shell=bash
# Variables here are used by the scripts that source this file.
# shellcheck disable=SC2034
# Shared by lab 4's verify scripts. Each script is read-only: it looks at
# the cluster, Postgres and Secrets Manager, and exits 0 when the step is
# done, or prints why not as its last line and exits 1.
set -uo pipefail

fail() {
  echo "$*"
  exit 1
}

ok() {
  echo "$*"
  exit 0
}

NS=lab04-app
TNS=lab04-traffic
APP_SECRET=lab04/db/app
ADMIN_SECRET=lab04/db/admin

# The aws CLI, as a pinned image through rootless Podman. --network host
# reaches Moto on 127.0.0.1; -e 'AWS_*' passes the endpoint, region and
# keys lab sets (locally: Moto and dummy keys).
AWS_IMAGE=docker.io/amazon/aws-cli:2.38.3@sha256:efe32e825cf203652eaca3e7e94679580274e7d567c39270aaa36a5144737108
aws() { podman run --rm --network host -e 'AWS_*' "$AWS_IMAGE" "$@"; }

need_cluster() {
  kubectl version --request-timeout=5s >/dev/null 2>&1 ||
    fail "the cluster does not answer (KUBECONFIG=${KUBECONFIG:-unset}): is the lab up? lab status"
}

# secret_value <name> [stage]: the secret's JSON in Secrets Manager, or "".
secret_value() {
  local stage="${2:-AWSCURRENT}"
  aws secretsmanager get-secret-value --secret-id "$1" --version-stage "$stage" \
    --query SecretString --output text 2>/dev/null
}

# sql <query>: run a query as Postgres's superuser over the pod's local
# socket (trusted, no password), one value per line.
sql() {
  kubectl -n "$NS" exec postgres-0 -c postgres -- psql -U postgres -d app -tAc "$1" 2>/dev/null
}

# login <user> <password>: connect as user through the Service, as the app
# does; the lab's pg_hba.conf asks every TCP connection for a password.
# Prints current_user, or Postgres's error. The password goes on stdin.
login() {
  # The single quotes are on purpose: the pod's shell expands these.
  # shellcheck disable=SC2016
  printf '%s\n' "$2" | kubectl -n "$NS" exec -i postgres-0 -c postgres -- env U="$1" sh -c \
    'read -r PGPASSWORD; export PGPASSWORD; psql -h postgres.lab04-app.svc -U "$U" -d app -tAc "select current_user" 2>&1 | tail -1'
}

# api <path>: GET the app through the API server's service proxy.
api() {
  kubectl get --raw "/api/v1/namespaces/$NS/services/sample-db:http/proxy$1" 2>/dev/null
}

# app_pod: the app's running pod, or "".
app_pod() {
  kubectl -n "$NS" get pods -l app=sample-db --field-selector=status.phase=Running \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null
}

# traffic_progress: the traffic Job's last progress line, or "".
traffic_progress() {
  kubectl -n "$TNS" logs job/traffic --tail=200 2>/dev/null | grep ' progress ' | tail -1
}
