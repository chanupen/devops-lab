#!/usr/bin/env bash
# Step 3: scale inflate to 2; consolidation removes the node they no longer need.
set -euo pipefail
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
kubectl -n "$NS" scale deployment inflate --replicas=2
wait_step 3 8
