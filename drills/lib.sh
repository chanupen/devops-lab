# shellcheck shell=bash
# Shared by the drills' scripts. lab runs them with KUBECONFIG set to the
# main lab, DRILL_ID, DRILL_DIR, DRILL_RUN, LAB_REGISTRY_PUSH and
# LAB_REGISTRY_PULL. lab saves the objects in drill.yaml's snapshot before
# inject.sh and puts them back before restore.sh.
set -uo pipefail

fail() {
  echo "$*"
  exit 1
}

ok() {
  echo "$*"
  exit 0
}

need_cluster() {
  kubectl version --request-timeout=5s >/dev/null 2>&1 ||
    fail "the cluster does not answer (KUBECONFIG=${KUBECONFIG:-unset}): is the lab up? lab status"
}

# argocd_core runs the argocd CLI in core mode: it talks to the API server
# with the lab's kubeconfig, no Argo CD login. Core mode reads the
# namespace from the kube context, so it gets a copy with namespace argocd
# and the learner's kubeconfig is left as it is.
argocd_core() {
  local kc rc
  kc=$(mktemp)
  kubectl config view --raw --minify >"$kc"
  KUBECONFIG="$kc" kubectl config set-context --current --namespace argocd >/dev/null
  KUBECONFIG="$kc" argocd --core "$@"
  rc=$?
  rm -f "$kc"
  return $rc
}

# release <app> <name=value>...: deploy a release the way a CD tool with
# Argo CD write-back does (Argo CD Image Updater, a pipeline running
# "argocd app set -p"): Helm parameters on the Application. Nothing goes
# to Git. lab-bootstrap, which renders the Application from Git, is told
# to leave the parameters and the auto-sync switch to the people who run
# the app (ignoreDifferences with RespectIgnoreDifferences), so self-heal
# keeps the release, and an emergency rollback can pause auto-sync.
release() {
  local app=$1
  shift
  kubectl -n argocd patch application lab-bootstrap --type merge -p "$(
    jq -cn --arg app "$app" '{spec: {
      ignoreDifferences: [{group: "argoproj.io", kind: "Application", name: $app, namespace: "argocd",
        jsonPointers: ["/spec/source/helm/parameters", "/spec/syncPolicy/automated"]}],
      syncPolicy: {syncOptions: ["RespectIgnoreDifferences=true"]}}}'
  )" >/dev/null || return 1
  local params='[]' kv
  for kv in "$@"; do
    params=$(jq -c --arg n "${kv%%=*}" --arg v "${kv#*=}" '. + [{name: $n, value: $v}]' <<<"$params")
  done
  kubectl -n argocd patch application "$app" --type merge \
    -p "{\"spec\":{\"source\":{\"helm\":{\"parameters\":$params}}}}" >/dev/null
}

# app_status <app>: "Synced/Healthy" and the like.
app_status() {
  kubectl -n argocd get application "$1" -o jsonpath='{.status.sync.status}/{.status.health.status}' 2>/dev/null
}

# wait_for <seconds> <what> <command...>: run the command every 3 seconds
# until it succeeds; fail with what after the time is up.
wait_for() {
  local secs=$1 what=$2
  shift 2
  local end=$((SECONDS + secs))
  until "$@" >/dev/null 2>&1; do
    [ $SECONDS -lt $end ] || fail "timed out after ${secs}s waiting for $what"
    sleep 3
  done
}

app_ready() { [ "$(app_status "$1")" = "Synced/Healthy" ]; }

# rolled_out <ns> <deployment>: every replica updated, ready and available.
rolled_out() {
  kubectl -n "$1" rollout status deployment "$2" --timeout=5s >/dev/null 2>&1
}

# wait_lab_back <app>...: after lab put the Applications back, wait until
# they and lab-bootstrap are Synced and Healthy again. A refresh makes Argo
# CD compare at once instead of at its next poll.
wait_lab_back() {
  local a
  for a in lab-bootstrap "$@"; do
    kubectl -n argocd annotate application "$a" argocd.argoproj.io/refresh=normal --overwrite >/dev/null 2>&1
  done
  for a in "$@" lab-bootstrap; do
    wait_for 300 "Application $a to be Synced/Healthy" app_ready "$a"
  done
}

# all_apps_ready: every Application is Synced and Healthy.
all_apps_ready() {
  [ "$(kubectl -n argocd get applications -o json |
    jq '[.items[] | select(.status.sync.status != "Synced" or .status.health.status != "Healthy")] | length')" = 0 ]
}
