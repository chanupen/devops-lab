# shellcheck shell=bash
# Variables here are used by the scripts that source this file.
# shellcheck disable=SC2034
# Shared by lab 6's verify scripts. Each script is read-only: it looks at
# the cluster (NodePools, NodeClaims, nodes, pods, events) and exits 0 when
# the step is done, or prints why not as its last line and exits 1.
set -uo pipefail

fail() {
  echo "$*"
  exit 1
}

ok() {
  echo "$*"
  exit 0
}

NS=lab06-karpenter
POOL=lab06
LABEL=lab.devops/lab=06-karpenter
# The cap: the lab's NodePool may never hold more than this many vCPUs.
MAX_CPU=6
# Each inflate pod's CPU request, in millicores. Two fit on a 2-vCPU node.
POD_CPU=700m

# The aws CLI, as a pinned image through rootless Podman, with the cloud
# session's keys and region that lab sets (lab labs env prints them).
AWS_IMAGE=docker.io/amazon/aws-cli:2.38.3@sha256:efe32e825cf203652eaca3e7e94679580274e7d567c39270aaa36a5144737108
aws() { podman run --rm --network host -e 'AWS_*' "$AWS_IMAGE" "$@"; }

need_cluster() {
  kubectl version --request-timeout=5s >/dev/null 2>&1 ||
    fail "the cluster does not answer (KUBECONFIG=${KUBECONFIG:-unset}): is the cloud lab up? lab status --mode cloud"
  kubectl get crd nodepools.karpenter.sh >/dev/null 2>&1 ||
    fail "this cluster has no Karpenter: lab 6 runs on the cloud lab (lab labs start 6 --mode cloud)"
}

# need_cap: the lab's NodePool exists and still has its CPU cap.
need_cap() {
  local cpu n
  kubectl get nodepool "$POOL" >/dev/null 2>&1 || fail "no NodePool $POOL: kubectl apply -f labs/06-karpenter/app/nodepool.yaml"
  cpu=$(kubectl get nodepool "$POOL" -o jsonpath='{.spec.limits.cpu}')
  case "$cpu" in
    "") fail "NodePool $POOL has no CPU limit: put limits.cpu: \"$MAX_CPU\" back (apply app/nodepool.yaml again)" ;;
    *m) n=$(((${cpu%m} + 999) / 1000)) ;;
    *[!0-9]*) fail "NodePool $POOL's CPU limit '$cpu' is not a plain number: use \"$MAX_CPU\"" ;;
    *) n=$cpu ;;
  esac
  [ "$n" -le "$MAX_CPU" ] || fail "NodePool $POOL's CPU limit is $cpu, above the lab's cap of $MAX_CPU: apply app/nodepool.yaml again"
}

# pool_nodes: the names of the lab's nodes that are not going away, one per
# line. A node Karpenter is removing has a deletion timestamp or the
# karpenter.sh/disrupted taint.
pool_nodes() {
  kubectl get nodes -l "karpenter.sh/nodepool=$POOL" -o json 2>/dev/null | jq -r '.items[]
    | select(.metadata.deletionTimestamp == null)
    | select([.spec.taints[]?.key] | index("karpenter.sh/disrupted") | not)
    | .metadata.name'
}

# claims: the lab's NodeClaims as "name deleting?" lines.
claims() {
  kubectl get nodeclaims -l "karpenter.sh/nodepool=$POOL" -o json 2>/dev/null |
    jq -r '.items[] | "\(.metadata.name) \(.metadata.deletionTimestamp != null)"'
}

# need_steady: no NodeClaim of the lab's pool is launching or going away,
# so the node count is final.
need_steady() {
  local nodes total deleting
  nodes=$(pool_nodes | grep -c .)
  total=$(claims | grep -c .)
  deleting=$(claims | grep -c ' true$')
  [ "$deleting" -eq 0 ] || fail "Karpenter is still removing $deleting node(s) of $POOL: wait and verify again"
  [ "$total" -eq "$nodes" ] || fail "Karpenter is still launching a node for $POOL ($total NodeClaims, $nodes ready nodes): wait and verify again"
}

# inflate_ready <n>: Deployment inflate wants n replicas and has n available.
inflate_ready() {
  local want have
  want=$(kubectl -n "$NS" get deployment inflate -o jsonpath='{.spec.replicas}' 2>/dev/null)
  [ -n "$want" ] || fail "no Deployment inflate in $NS: kubectl apply -f labs/06-karpenter/app/inflate.yaml"
  [ "$want" = "$1" ] || fail "Deployment inflate wants $want replicas, not $1: kubectl -n $NS scale deployment inflate --replicas=$1"
  have=$(kubectl -n "$NS" get deployment inflate -o jsonpath='{.status.availableReplicas}' 2>/dev/null)
  [ "${have:-0}" = "$1" ] || fail "Deployment inflate has ${have:-0} of $1 pods available: watch kubectl -n $NS get pods -o wide"
  local req
  req=$(kubectl -n "$NS" get deployment inflate -o jsonpath='{.spec.template.spec.containers[0].resources.requests.cpu}')
  [ "$req" = "$POD_CPU" ] || fail "inflate requests $req CPU per pod; the lab's numbers assume $POD_CPU: apply app/inflate.yaml again"
}

# pods_per_node: "node count" lines for inflate's running pods.
pods_per_node() {
  kubectl -n "$NS" get pods -l app=inflate --field-selector=status.phase=Running \
    -o jsonpath='{range .items[*]}{.spec.nodeName}{"\n"}{end}' 2>/dev/null | sort | uniq -c | awk '{print $2, $1}'
}

# events <reason>: Karpenter's events with that reason, as "kind name time
# message" lines (time is the last time it was seen).
events() {
  kubectl get events -A --field-selector "reason=$1" -o json 2>/dev/null | jq -r '.items[]
    | "\(.involvedObject.kind) \(.involvedObject.name) \(.lastTimestamp // .eventTime // .metadata.creationTimestamp) \(.message)"'
}

# interrupt_events: Karpenter's interruption events (SpotInterrupted and
# the termination it starts), as "kind name" lines. The reason is matched
# loosely, so a renamed reason in a newer Karpenter still counts.
interrupt_events() {
  kubectl get events -A -o json 2>/dev/null | jq -r '.items[]
    | select(.reason | test("interrupt"; "i"))
    | "\(.involvedObject.kind) \(.involvedObject.name)"' | sort -u
}
