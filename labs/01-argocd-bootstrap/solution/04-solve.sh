#!/usr/bin/env bash
# Step 4: automated sync with self-heal and prune.
set -euo pipefail
# The same with the CLI: argocd app set podinfo --sync-policy automated --auto-prune --self-heal
kubectl -n argocd patch application podinfo --type merge \
  -p '{"spec":{"syncPolicy":{"automated":{"prune":true,"selfHeal":true}}}}'
kubectl -n argocd get application podinfo -o jsonpath='{.spec.syncPolicy.automated}{"\n"}'
