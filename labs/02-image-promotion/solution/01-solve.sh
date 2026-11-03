#!/usr/bin/env bash
# Step 1: copy the app to a work directory and build it with rootless Podman.
set -euo pipefail
work="${LAB_WORK:-$HOME/lab02}"
rm -rf "$work" && cp -r "$(dirname "$0")/../app" "$work"
podman build -t lab02-hello:0.1.0 "$work"
