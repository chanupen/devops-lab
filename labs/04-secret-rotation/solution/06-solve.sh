#!/usr/bin/env bash
# Step 6: retire app_v1. Open sessions survive a password change or a
# drop, so check pg_stat_activity first: the app closed its old pool when
# it switched, so there should be none; end any leftover. app_v1 owns
# nothing (the group role app holds the privileges), so DROP ROLE works.
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
set -e
kubectl -n "$NS" exec -i postgres-0 -c postgres -- psql -v ON_ERROR_STOP=1 -U postgres -d app <<'SQL'
SELECT pid, application_name, backend_start FROM pg_stat_activity WHERE usename = 'app_v1';
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = 'app_v1';
DROP ROLE app_v1;
SQL
# Let the traffic Job report once more after the drop.
sleep 12
