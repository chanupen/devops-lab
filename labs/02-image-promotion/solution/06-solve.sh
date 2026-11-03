#!/usr/bin/env bash
# Step 6: scan the digest prod runs, compare it with what both pods run, and
# record it on the prod Deployment.
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
running=$(kubectl -n lab02-prod get pods -l app=hello -o jsonpath='{.items[0].status.containerStatuses[0].imageID}')
digest=${running##*@}
dev=$(kubectl -n lab02-dev get pods -l app=hello -o jsonpath='{.items[0].status.containerStatuses[0].imageID}')
[ "${dev##*@}" = "$digest" ] || { echo "dev runs ${dev##*@}, prod runs $digest"; exit 1; }
podman run --rm --network host -v lab02-trivy-cache:/root/.cache "$TRIVY_IMAGE" \
  image --image-src remote --insecure --severity HIGH,CRITICAL --exit-code 1 "$PUSH/lab02-hello@$digest"
kubectl -n lab02-prod annotate deployment hello --overwrite "lab.devops/scanned-digest=$digest"
