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

# good_release <app>: make sure Argo CD's history has a release without
# Helm parameters (the release from Git), so there is one to roll back to.
# History lives in the Application's status and can be empty (an app made
# again, its status cleared); a sync of what runs now records one.
good_release() {
  local n
  n=$(kubectl -n argocd get application "$1" -o json |
    jq '[.status.history // [] | .[] | select((.source.helm.parameters // []) | length == 0)] | length')
  [ "${n:-0}" -gt 0 ] && return 0
  echo "recording the running release of $1 in Argo CD's history first"
  argocd_core app sync "$1" --timeout 180 >/dev/null
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

# sm <Action> <json>: one Secrets Manager call to Moto (the local
# provider's AWS emulator on 127.0.0.1:4566), through its JSON API with
# curl, so the scripts need no aws image. Moto checks no signature; the
# credential scope only sets the region. Prints the response; fails on an
# API error.
sm() {
  local out
  out=$(curl -s --max-time 10 -H 'Content-Type: application/x-amz-json-1.1' -H "X-Amz-Target: secretsmanager.$1" \
    -H "Authorization: AWS4-HMAC-SHA256 Credential=lab-dummy/20260101/${AWS_REGION:-ap-south-1}/secretsmanager/aws4_request, SignedHeaders=host, Signature=0" \
    -d "$2" "${AWS_ENDPOINT_URL_SECRETS_MANAGER:-http://127.0.0.1:4566}/") || return 1
  printf '%s\n' "$out"
  ! jq -e '.__type? // empty' <<<"$out" >/dev/null 2>&1
}

# kind_node_running <name>: the kind node's container runs.
kind_node_running() { [ "$(podman inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" = true ]; }

# node_ready <name>: the Node reports Ready.
node_ready() {
  [ "$(kubectl get node "$1" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)" = True ]
}

# pod_netns <node> <namespace> <container>: run the rest of the command
# line inside the network namespace of that container on that kind node
# (tc, ip). The node is root in its user namespace and owns the pod's
# network namespace, so this needs no privileged pod and no sudo.
pod_netns() {
  local node=$1 ns=$2 name=$3
  shift 3
  podman exec "$node" sh -c '
    cid=$(crictl ps -q --name "^$1\$" --label "io.kubernetes.pod.namespace=$2" | head -1)
    [ -n "$cid" ] || { echo "no running container $1 in $2 on this node" >&2; exit 3; }
    pid=$(crictl inspect -o go-template --template "{{.info.pid}}" "$cid")
    shift 2
    exec nsenter -t "$pid" -n "$@"' sh "$name" "$ns" "$@"
}

# kind_workers: the lab's kind worker nodes.
kind_workers() { kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o jsonpath='{.items[*].metadata.name}'; }
