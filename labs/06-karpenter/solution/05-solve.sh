#!/usr/bin/env bash
# Step 5: let the PDB allow one pod at a time; consolidation goes ahead.
set -euo pipefail
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
kubectl -n "$NS" patch pdb inflate --type merge -p '{"spec":{"maxUnavailable":1}}'
wait_step 5 8
