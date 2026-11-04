#!/usr/bin/env bash
# Step 4: Secrets Manager's current value is app_v2 with a password that
# logs in; the previous version (app_v1) is kept.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
cur=$(secret_value "$APP_SECRET")
[ "$(jq -r '.username // empty' <<<"$cur" 2>/dev/null)" = app_v2 ] ||
  fail "$APP_SECRET's current value is not for app_v2: aws secretsmanager put-secret-value"
[ "$(login app_v2 "$(jq -r .password <<<"$cur")")" = app_v2 ] ||
  fail "the password in $APP_SECRET does not log in as app_v2: it must be the one from step 3"
prev=$(secret_value "$APP_SECRET" AWSPREVIOUS)
[ "$(jq -r '.username // empty' <<<"$prev" 2>/dev/null)" = app_v1 ] ||
  fail "$APP_SECRET has no previous version for app_v1: update the secret with put-secret-value, do not delete and re-create it"
ok "Secrets Manager holds app_v2 (current) and app_v1 (previous)"
