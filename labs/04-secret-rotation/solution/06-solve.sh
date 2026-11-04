#!/usr/bin/env bash
# Step 6: retire app_v1: no new logins, and end any session it still has.
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
set -e
kubectl -n "$NS" exec -i postgres-0 -c postgres -- psql -v ON_ERROR_STOP=1 -U postgres -d app <<'SQL'
ALTER ROLE app_v1 NOLOGIN;
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = 'app_v1';
SQL
# Let the traffic Job report once more after the revoke.
sleep 12
