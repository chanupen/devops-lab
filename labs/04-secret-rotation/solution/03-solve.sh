#!/usr/bin/env bash
# Step 3: add the second login role, app_v2, to the group role app. Its
# password is kept in a file for step 4 (mode 0600), never in a command line.
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
set -e
work="${LAB_WORK:-$HOME/lab04}"
mkdir -p "$work"
umask 077
[ -s "$work/app_v2.password" ] || head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n' >"$work/app_v2.password"
# psql reads the password as a variable from stdin, so it is quoted safely.
{
  printf '\\set p %s\n' "$(cat "$work/app_v2.password")"
  cat <<'SQL'
SELECT NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'app_v2') AS missing \gset
\if :missing
  CREATE ROLE app_v2 LOGIN PASSWORD :'p' IN ROLE app;
\else
  ALTER ROLE app_v2 LOGIN PASSWORD :'p';
\endif
SQL
} | kubectl -n "$NS" exec -i postgres-0 -c postgres -- psql -v ON_ERROR_STOP=1 -U postgres -d app
