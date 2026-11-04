#!/usr/bin/env bash
# Step 6: app_v1 is retired and its password rejected, and the traffic Job
# ran through the whole rotation with zero failed requests.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
[ "$(sql "select rolcanlogin from pg_roles where rolname = 'app_v1'")" != t ] ||
  fail "app_v1 can still log in: ALTER ROLE app_v1 NOLOGIN (or DROP ROLE app_v1)"
n=$(sql "select count(*) from pg_stat_activity where usename = 'app_v1'")
[ "$n" = 0 ] || fail "$n session(s) of app_v1 are still open: NOLOGIN stops new logins only; end them with pg_terminate_backend"
prev=$(secret_value "$APP_SECRET" AWSPREVIOUS)
[ -n "$prev" ] || fail "no previous version of $APP_SECRET to test the old password with"
out=$(login "$(jq -r .username <<<"$prev")" "$(jq -r .password <<<"$prev")")
[[ "$out" == *FATAL* ]] || fail "the old credentials still log in: got '$out'"
[ "$(api /api/check | jq -r '.user // empty' 2>/dev/null)" = app_v2 ] || fail "the app does not answer as app_v2"
line=$(traffic_progress)
[ -n "$line" ] || fail "the traffic Job has no progress line: is it still running?"
age=$(($(date +%s) - $(date -d "${line%% *}" +%s)))
[ "$age" -le 30 ] || fail "the traffic Job's last progress line is ${age}s old: it must run until after the revoke"
started=$(kubectl -n "$TNS" get pods -l app=traffic -o jsonpath='{.items[0].status.startTime}')
switched=$(kubectl -n "$NS" logs deployment/sample-db 2>/dev/null | jq -r 'select(.msg == "moved to new credentials") | .time' | tail -1)
[ -n "$switched" ] || fail "the app's log has no 'moved to new credentials' line"
[ "$(date -d "$started" +%s)" -lt "$(date -d "$switched" +%s)" ] ||
  fail "the traffic Job started after the switch ($switched): it did not watch the rotation"
failed=$(sed -n 's/.*failed=\([0-9]*\).*/\1/p' <<<"$line")
[ "$failed" = 0 ] || fail "the traffic Job saw $failed failed requests during the rotation: kubectl -n $TNS logs job/traffic | grep FAILED"
ok "app_v1 is rejected (${out##*FATAL:  }); traffic: ${line##* progress }"
