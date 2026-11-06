#!/usr/bin/env bash
# Step 1: copy the app to a work directory and build it with rootless Podman
# for the nodes' platform (linux/arm64 in cloud mode, under emulation).
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
work="${LAB_WORK:-$HOME/lab02}"
rm -rf "$work" && cp -r "$(dirname "$0")/../app" "$work"
podman build --platform "$PLATFORM" -t lab02-hello:0.1.0 "$work"
