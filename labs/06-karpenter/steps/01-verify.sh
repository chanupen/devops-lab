#!/usr/bin/env bash
# Step 1: NodePool lab06 is applied as given (cap, taint) and Ready, and the
# answers about the cluster's NodePool spot match it.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
need_cap
kubectl get nodepool "$POOL" -o jsonpath='{.metadata.labels.lab\.devops/lab}' | grep -qx 06-karpenter ||
  fail "NodePool $POOL has no label $LABEL: apply app/nodepool.yaml as it is"
effect=$(kubectl get nodepool "$POOL" -o jsonpath='{.spec.template.spec.taints[?(@.key=="lab.devops/lab")].effect}')
[ "$effect" = NoSchedule ] || fail "NodePool $POOL lost its taint lab.devops/lab=06-karpenter:NoSchedule: apply app/nodepool.yaml as it is"
ready=$(kubectl get nodepool "$POOL" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')
[ "$ready" = True ] || fail "NodePool $POOL is not Ready: kubectl describe nodepool $POOL"
kubectl get namespace "$NS" >/dev/null 2>&1 || fail "no namespace $NS: kubectl apply -f labs/06-karpenter/app/namespace.yaml"
kubectl get nodepool spot >/dev/null 2>&1 || fail "the cluster has no NodePool spot: is the cluster stack applied? lab status --mode cloud"
answer() { kubectl -n "$NS" get configmap answers -o jsonpath="{.data.$1}" 2>/dev/null; }
kubectl -n "$NS" get configmap answers >/dev/null 2>&1 || fail "no ConfigMap answers in $NS: kubectl -n $NS create configmap answers --from-literal=cpu-limit=... --from-literal=consolidate-after=..."
want=$(kubectl get nodepool spot -o jsonpath='{.spec.limits.cpu}')
[ "$(answer cpu-limit)" = "$want" ] || fail "answers.cpu-limit is '$(answer cpu-limit)': read spec.limits of NodePool spot again"
want=$(kubectl get nodepool spot -o jsonpath='{.spec.disruption.consolidateAfter}')
[ "$(answer consolidate-after)" = "$want" ] || fail "answers.consolidate-after is '$(answer consolidate-after)': read spec.disruption of NodePool spot again"
ok "NodePool $POOL is Ready with a cap of $MAX_CPU vCPUs, and your answers match NodePool spot"
