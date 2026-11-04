#!/usr/bin/env bash
# Step 3: a second login role app_v2 exists, in the group role app, while
# app_v1 still logs in (dual credentials).
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
[ "$(sql "select rolcanlogin from pg_roles where rolname = 'app_v2'")" = t ] ||
  fail "no role app_v2 that can log in: CREATE ROLE app_v2 LOGIN PASSWORD '...' IN ROLE app"
[ "$(sql "select pg_has_role('app_v2', 'app', 'MEMBER')")" = t ] || fail "app_v2 is not a member of the group role app: GRANT app TO app_v2"
[ "$(sql "select rolsuper from pg_roles where rolname = 'app_v2'")" = f ] || fail "app_v2 is a superuser: the app needs only the group's privileges"
v1=$(secret_value "$APP_SECRET" AWSCURRENT)
if [ "$(jq -r .username <<<"$v1")" = app_v1 ]; then
  [ "$(login app_v1 "$(jq -r .password <<<"$v1")")" = app_v1 ] || fail "app_v1 no longer logs in: both roles must work until the app has switched"
fi
ok "app_v2 can log in and is a member of app; app_v1 still works"
