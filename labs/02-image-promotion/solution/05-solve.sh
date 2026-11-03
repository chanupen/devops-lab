#!/usr/bin/env bash
# Step 5: promote by copying the dev digest to the prod tag (no rebuild),
# then deploy that digest to lab02-prod.
set -euo pipefail
. "$(dirname "$0")/../steps/lib.sh"
skopeo() { podman run --rm --network host "$SKOPEO_IMAGE" "$@"; }
digest=$(skopeo inspect --tls-verify=false --format '{{.Digest}}' "docker://$PUSH/lab02-hello:dev")
skopeo copy --preserve-digests --src-tls-verify=false --dest-tls-verify=false \
  "docker://$PUSH/lab02-hello@$digest" "docker://$PUSH/lab02-hello:prod"
kubectl create namespace lab02-prod --dry-run=client -o yaml | kubectl apply -f -
sed "s#NAMESPACE#lab02-prod#; s#IMAGE#$PULL/lab02-hello@$digest#" "$(dirname "$0")/04-deployment.yaml" | kubectl apply -f -
kubectl -n lab02-prod rollout status deployment/hello --timeout=180s
