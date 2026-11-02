#!/usr/bin/env bash
# Step 1: install Argo CD with Helm, chart pinned, and wait for the pods.
set -euo pipefail
helm install argocd argo-cd \
  --repo https://argoproj.github.io/argo-helm \
  --version 10.10.5 \
  --namespace argocd --create-namespace \
  --wait --timeout 10m
kubectl -n argocd get pods
