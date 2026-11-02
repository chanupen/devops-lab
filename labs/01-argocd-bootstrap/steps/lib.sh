# shellcheck shell=bash
# Shared by the verify scripts. Each script is read-only: it looks at the
# cluster and exits 0 when the step is done, or prints why not as its last
# line and exits 1. lab runs it with KUBECONFIG set to the lab's cluster.
set -uo pipefail

fail() {
  echo "$*"
  exit 1
}

ok() {
  echo "$*"
  exit 0
}

# need_cluster fails when the cluster does not answer.
need_cluster() {
  kubectl version --request-timeout=5s >/dev/null 2>&1 ||
    fail "the lab cluster does not answer (KUBECONFIG=${KUBECONFIG:-unset}): is the lab started? lab labs status"
}

# app <name>: the Application's JSON, or fail.
app() {
  kubectl -n argocd get application "$1" -o json 2>/dev/null ||
    fail "no Application $1 in namespace argocd"
}
