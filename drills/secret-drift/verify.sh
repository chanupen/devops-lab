#!/usr/bin/env bash
# Recovered: Secrets Manager serves the app's secret again with the value
# and versions it had (the database password never changed), ESO has
# synced it, the Kubernetes Secret matches it and the app logs in. Until
# ESO syncs again, the message describes the symptom only.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
id=lab/sample-db/app
saved="$DRILL_STATE/moto.json"
[ -s "$saved" ] || fail "the drill's saved state is missing ($saved): lab drills abort"
es=$(kubectl -n sample-db get externalsecret db-app -o json) || fail "no ExternalSecret db-app in sample-db"
ready=$(jq -r '.status.conditions[]? | select(.type == "Ready") | .status' <<<"$es")
if ! val=$(sm GetSecretValue "$(jq -cn --arg id "$id" '{SecretId: $id}')" 2>/dev/null) || [ "$ready" != True ]; then
  fail "the app's database credentials still do not sync from Secrets Manager"
fi
want=$(jq -r '.versions[] | select(.stages | index("AWSCURRENT")) | .value' "$saved")
[ "$(jq -r .SecretString <<<"$val")" = "$want" ] ||
  fail "the secret in Secrets Manager does not hold the password the database has: the app would fail at its next login with it. Bring back the original value"
have=$(sm ListSecretVersionIds "$(jq -cn --arg id "$id" '{SecretId: $id}')" | jq -c '[.Versions[].VersionId] | sort')
[ "$have" = "$(jq -c '[.versions[].id] | sort' "$saved")" ] ||
  fail "the secret serves the right password, but it was made again: its version history is lost. Secrets Manager can bring back the secret it had"
synced=$(jq -r '.status.refreshTime // ""' <<<"$es")
deleted_at=$(stat -c %Y "$saved")
[ -n "$synced" ] && [ "$(date -d "$synced" +%s)" -gt "$deleted_at" ] || fail "ESO has not synced since: wait a minute, or force a sync"
kpass=$(kubectl -n sample-db get secret db-app -o jsonpath='{.data.password}' | base64 -d)
[ "$kpass" = "$(jq -r .password <<<"$want")" ] || fail "the Kubernetes Secret db-app does not hold the password in Secrets Manager"
out=$(kubectl get --raw /api/v1/namespaces/sample-db/services/sample-db:8080/proxy/api/check 2>&1) ||
  fail "sample-db does not answer /api/check: $(tail -c 160 <<<"$out")"
ok "Secrets Manager serves $id with its versions, ESO synced it, and sample-db logs in as $(jq -r .user <<<"$out")"
