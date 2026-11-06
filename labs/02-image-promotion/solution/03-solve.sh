#!/usr/bin/env bash
# Step 3: scan dev with Trivy, move the Dockerfile to the patched base,
# rebuild, scan again until there is nothing HIGH or CRITICAL, push as dev.
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
need_registry
registry_login
work="${LAB_WORK:-$HOME/lab02}"
# The old base: Trivy lists HIGH and CRITICAL CVEs (and exits 1 for them).
trivy --severity HIGH,CRITICAL --exit-code 1 "$PUSH/lab02-hello:dev" && { echo "expected CVEs in the old base"; exit 1; }
sed -i "s#^FROM .*#FROM $FIXED_FROM#" "$work/Dockerfile"
podman build --platform "$PLATFORM" -t lab02-hello:0.2.0 "$work"
podman push --tls-verify="$TLS" lab02-hello:0.2.0 "$PUSH/lab02-hello:dev"
# The patched image: nothing HIGH or CRITICAL, so exit code 0.
trivy --severity HIGH,CRITICAL --exit-code 1 "$PUSH/lab02-hello:dev"
