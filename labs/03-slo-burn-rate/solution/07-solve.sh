#!/usr/bin/env bash
# Step 7: generate the same SLO's rules with Sloth and apply them.
set -euo pipefail
cd "$(dirname "$0")"
SLOTH_IMAGE=ghcr.io/slok/sloth:v0.16.0@sha256:f0f0075b0d45c1cf684e92947508cc1d5bf573925f785f803a439b2306c8b9a5
podman run --rm -v "$PWD":/work:Z "$SLOTH_IMAGE" generate -i /work/07-slo.yaml 2>/dev/null | kubectl apply -f -
