#!/usr/bin/env bash
# Step 2: deploy inflate and scale it to 4; Karpenter launches two spot
# nodes for the pending pods.
set -euo pipefail
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
kubectl apply -f "$APP/inflate.yaml"
kubectl -n "$NS" scale deployment inflate --replicas=4
wait_step 2 8
