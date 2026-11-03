#!/usr/bin/env bash
# Step 3: the same image by digest is allowed. Run it as Pod web.
set -euo pipefail
kubectl -n lab05-apps run web --restart=Never \
  --image=ghcr.io/stefanprodan/podinfo:6.15.0@sha256:ec73780a8425f59ea49f5bc8cdff0d598805a224fbaa1f86c67a244f250fa9da
kubectl -n lab05-apps wait --for=condition=Ready pod/web --timeout=180s
