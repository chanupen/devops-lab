#!/usr/bin/env bash
# Step 3: scan dev with Trivy, move the Dockerfile to the patched base,
# rebuild, scan again until there is nothing HIGH or CRITICAL, push as dev.
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
work="${LAB_WORK:-$HOME/lab02}"
trivy() {
  podman run --rm --network host -v lab02-trivy-cache:/root/.cache "$TRIVY_IMAGE" image --image-src remote --insecure "$@"
}
# The old base: Trivy lists HIGH and CRITICAL CVEs (and exits 1 for them).
trivy --severity HIGH,CRITICAL --exit-code 1 "$PUSH/lab02-hello:dev" && { echo "expected CVEs in the old base"; exit 1; }
sed -i "s#^FROM .*#FROM $FIXED_FROM#" "$work/Dockerfile"
podman build -t lab02-hello:0.2.0 "$work"
podman push --tls-verify=false lab02-hello:0.2.0 "$PUSH/lab02-hello:dev"
# The patched image: nothing HIGH or CRITICAL, so exit code 0.
trivy --severity HIGH,CRITICAL --exit-code 1 "$PUSH/lab02-hello:dev"
