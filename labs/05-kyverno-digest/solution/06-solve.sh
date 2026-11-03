#!/usr/bin/env bash
# Step 6: put the policy, two Pods and the expected results in a work
# directory, and run kyverno test on it offline.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$here/../steps/lib.sh"
dir="${LAB_WORK:-$HOME/lab05}/test"
mkdir -p "$dir"
cp "$here/01-policy.yaml" "$dir/policy.yaml"
cp "$here/06-resources.yaml" "$dir/resources.yaml"
cp "$here/06-kyverno-test.yaml" "$dir/kyverno-test.yaml"
podman run --rm --network none -v "$dir":/test:Z,ro "$KYVERNO_CLI" test /test
