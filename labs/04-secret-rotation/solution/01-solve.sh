#!/usr/bin/env bash
# Step 1: create the credentials in Secrets Manager, deploy the lab's copy
# of the app with the sample-db stack's image, and record the pod's UID.
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
set -e
app="$(dirname "$0")/../app"
pw() { head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
for s in "$ADMIN_SECRET:postgres" "$APP_SECRET:app_v1"; do
  name=${s%%:*} user=${s##*:}
  aws secretsmanager describe-secret --secret-id "$name" >/dev/null 2>&1 ||
    aws secretsmanager create-secret --name "$name" \
      --secret-string "$(jq -cn --arg u "$user" --arg p "$(pw)" '{username: $u, password: $p}')" >/dev/null
done
img=$(kubectl -n sample-db get deployment sample-db -o jsonpath='{.spec.template.spec.containers[0].image}')
kubectl apply -f "$app/namespaces.yaml" -f "$app/externalsecrets.yaml" -f "$app/postgres.yaml"
sed "s|SAMPLE_DB_IMAGE|$img|" "$app/app.yaml" | kubectl apply -f -
kubectl -n "$NS" wait externalsecret/db-app externalsecret/db-admin --for=condition=Ready --timeout=120s
kubectl -n "$NS" rollout status statefulset/postgres --timeout=180s
kubectl -n "$NS" rollout status deployment/sample-db --timeout=180s
uid=$(kubectl -n "$NS" get pods -l app=sample-db -o jsonpath='{.items[0].metadata.uid}')
kubectl -n "$NS" annotate deployment sample-db --overwrite "lab.devops/pod-uid=$uid"
