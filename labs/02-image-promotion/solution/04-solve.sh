#!/usr/bin/env bash
# Step 4: deploy dev's digest (not its tag) to lab02-dev.
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
need_registry
digest=$(skopeo inspect --tls-verify="$TLS" --format '{{.Digest}}' "docker://$PUSH/lab02-hello:dev")
kubectl create namespace lab02-dev --dry-run=client -o yaml | kubectl apply -f -
sed "s#NAMESPACE#lab02-dev#; s#IMAGE#$PULL/lab02-hello@$digest#" "$(dirname "$0")/04-deployment.yaml" | kubectl apply -f -
kubectl -n lab02-dev rollout status deployment/hello --timeout=180s
