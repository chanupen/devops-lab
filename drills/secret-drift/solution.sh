#!/usr/bin/env bash
# The reference fix: see the secret is scheduled for deletion, cancel the
# deletion (same value, same versions), and let ESO sync at once.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
kubectl -n sample-db get externalsecret db-app
sm DescribeSecret '{"SecretId":"lab/sample-db/app"}' | jq '{Name, DeletedDate}'
sm RestoreSecret '{"SecretId":"lab/sample-db/app"}' | jq -c .
kubectl -n sample-db annotate externalsecret db-app force-sync="$(date +%s)" --overwrite
synced() { [ "$(kubectl -n sample-db get externalsecret db-app -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = True ]; }
wait_for 120 "ExternalSecret db-app to sync" synced
kubectl -n sample-db get externalsecret db-app
