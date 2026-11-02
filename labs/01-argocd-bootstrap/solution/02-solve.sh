#!/usr/bin/env bash
# Step 2: port-forward the API and log in as admin.
set -euo pipefail
# By hand, run this in a second terminal and keep it open:
#   kubectl -n argocd port-forward svc/argocd-server 18080:443
# Here it runs in the background; its PID goes to $TMPDIR/lab-01-port-forward.pid.
pidfile="${TMPDIR:-/tmp}/lab-01-port-forward.pid"
if ! { [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null; }; then
  setsid kubectl -n argocd port-forward svc/argocd-server 18080:443 >/dev/null 2>&1 &
  echo $! >"$pidfile"
fi
for _ in $(seq 30); do
  curl -ks -o /dev/null https://localhost:18080/healthz && break
  sleep 1
done
password=$(kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)
argocd login localhost:18080 --username admin --password "$password" --insecure
argocd account get-user-info
