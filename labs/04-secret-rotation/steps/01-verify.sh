#!/usr/bin/env bash
# Step 1: the lab's copy of the app runs in lab04-app with credentials from
# Secrets Manager, mounted as a Secret volume, and its pod UID is recorded.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
for s in "$APP_SECRET" "$ADMIN_SECRET"; do
  [ -n "$(secret_value "$s")" ] || fail "no secret $s in Secrets Manager: create it with aws secretsmanager create-secret"
done
for es in db-app db-admin; do
  r=$(kubectl -n "$NS" get externalsecret "$es" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)
  [ "$r" = True ] || fail "ExternalSecret $es in $NS is not Ready: kubectl -n $NS describe externalsecret $es"
done
kubectl -n "$NS" rollout status statefulset/postgres --timeout=5s >/dev/null 2>&1 || fail "Postgres in $NS is not ready yet"
kubectl -n "$NS" rollout status deployment/sample-db --timeout=5s >/dev/null 2>&1 || fail "Deployment sample-db in $NS is not available yet"
img=$(kubectl -n "$NS" get deployment sample-db -o jsonpath='{.spec.template.spec.containers[0].image}')
[[ "$img" == *@sha256:* ]] || fail "the app's image is '$img': use the sample-db stack's image, by digest"
sub=$(kubectl -n "$NS" get deployment sample-db -o json | jq -r '[.spec.template.spec.containers[].volumeMounts[]? | select(.subPath or .subPathExpr)] | length')
[ "$sub" = 0 ] || fail "a volume mount uses subPath: the kubelet never updates such a file"
user=$(api /api/check | jq -r '.user // empty' 2>/dev/null)
[ "$user" = app_v1 ] || fail "the app answers as '${user:-nothing}', want app_v1: curl it through the service proxy"
pod=$(app_pod)
uid=$(kubectl -n "$NS" get pod "$pod" -o jsonpath='{.metadata.uid}')
rec=$(kubectl -n "$NS" get deployment sample-db -o jsonpath='{.metadata.annotations.lab\.devops/pod-uid}')
[ "$rec" = "$uid" ] || fail "record the pod's UID on the Deployment: annotation lab.devops/pod-uid=$uid (now '${rec}')"
ok "sample-db runs as app_v1 from Secrets Manager; pod $pod ($uid) recorded"
