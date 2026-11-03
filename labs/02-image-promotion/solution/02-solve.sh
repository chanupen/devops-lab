#!/usr/bin/env bash
# Step 2: push the image to the lab registry as dev. Zot speaks plain HTTP.
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
podman push --tls-verify=false lab02-hello:0.1.0 "$PUSH/lab02-hello:dev"
