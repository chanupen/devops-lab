#!/usr/bin/env bash
# Step 2: create the namespace and try a tag-only Pod with a server-side
# dry run: the API server sends it to Kyverno's webhook, which denies it.
set -uo pipefail
kubectl create namespace lab05-apps --dry-run=client -o yaml | kubectl label --local -f - lab.devops/lab=05-kyverno-digest -o yaml | kubectl apply -f -
if kubectl -n lab05-apps run tagged --image=ghcr.io/stefanprodan/podinfo:6.15.0 --restart=Never --dry-run=server; then
  echo "the tag-only Pod was allowed"
  exit 1
fi
