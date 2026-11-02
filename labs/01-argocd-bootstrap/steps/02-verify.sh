#!/usr/bin/env bash
# Step 2: the argocd CLI is logged in, and the API answers through the
# port-forward.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
command -v argocd >/dev/null || fail "the argocd CLI is not on PATH: see the lab README"
server=$(argocd context 2>/dev/null | awk '$1 == "*" {print $3}')
[ -n "$server" ] || fail "the argocd CLI is not logged in: argocd login localhost:18080 --insecure"
info=$(timeout 20 argocd account get-user-info 2>&1) ||
  fail "the API does not answer on $server: is the port-forward running? ($(tail -n1 <<<"$info"))"
grep -q "Logged In: true" <<<"$info" || fail "the API answers on $server, but the session is not logged in: log in again"
ok "logged in to Argo CD at $server as $(awk '/^Username:/ {print $2}' <<<"$info")"
