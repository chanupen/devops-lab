#!/usr/bin/env bash
# lab has put the ExternalSecret and the Kubernetes Secret back. Put the
# secret in Moto back exactly as the run saved it: cancel a pending
# deletion, or create it again with the same version IDs and values, and
# point AWSCURRENT (and AWSPREVIOUS) where they were. Then let ESO sync
# once and check the app logs in. The database password never changes.
# shellcheck disable=SC2016 # jq programs, not shell expansions
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
saved="$DRILL_STATE/moto.json"
if [ ! -s "$saved" ]; then
  echo "no saved Moto state ($saved): inject did not get that far, nothing to put back in Moto"
  exit 0
fi
id=$(jq -r .secret "$saved")
q() { jq -cn --arg id "$id" "$@"; }
desc=$(sm DescribeSecret "$(q '{SecretId: $id}')" 2>/dev/null) || desc=""
if [ -n "$desc" ] && [ -n "$(jq -r '.DeletedDate // empty' <<<"$desc")" ]; then
  echo "cancelling the deletion of $id"
  sm RestoreSecret "$(q '{SecretId: $id}')" >/dev/null || fail "restore-secret $id failed"
  desc=$(sm DescribeSecret "$(q '{SecretId: $id}')")
fi
same() { [ "$(jq -c '[.Versions[].VersionId] | sort' <<<"$1")" = "$(jq -c '[.versions[].id] | sort' "$saved")" ]; }
versions=$(sm ListSecretVersionIds "$(q '{SecretId: $id}')" 2>/dev/null) || versions='{"Versions":[]}'
if [ -n "$desc" ] && ! same "$versions" && ! jq -e --slurpfile s "$saved" \
  '[.Versions[].VersionId] as $have | all($s[0].versions[].id; . as $v | $have | index($v))' <<<"$versions" >/dev/null; then
  echo "$id was made again without its versions: deleting it to create it as it was"
  sm DeleteSecret "$(q '{SecretId: $id, ForceDeleteWithoutRecovery: true}')" >/dev/null || fail "could not delete $id to create it again"
  desc=""
fi
if [ -z "$desc" ]; then
  echo "creating $id again with its saved versions"
  first=1
  while read -r v; do
    tok=$(jq -r .id <<<"$v")
    body=$(q --arg t "$tok" --arg s "$(jq -r .value <<<"$v")" '{SecretId: $id, Name: $id, ClientRequestToken: $t, SecretString: $s}')
    if [ $first = 1 ]; then
      sm CreateSecret "$body" >/dev/null || fail "create-secret $id failed"
      first=0
    else
      sm PutSecretValue "$body" >/dev/null || fail "put-secret-value $id failed"
    fi
  done < <(jq -c '.versions[]' "$saved")
fi
# Stages: each saved stage back on its saved version (a learner's
# put-secret-value may have moved AWSCURRENT to a new version).
while read -r v; do
  tok=$(jq -r .id <<<"$v")
  for stage in $(jq -r '.stages[]' <<<"$v"); do
    cur=$(sm DescribeSecret "$(q '{SecretId: $id}')" | jq -r --arg s "$stage" '.VersionIdsToStages | to_entries[] | select(.value | index($s)) | .key' | head -1)
    [ "$cur" = "$tok" ] && continue
    body=$(q --arg s "$stage" --arg t "$tok" --arg c "$cur" '{SecretId: $id, VersionStage: $s, MoveToVersionId: $t} + (if $c == "" then {} else {RemoveFromVersionId: $c} end)')
    sm UpdateSecretVersionStage "$body" >/dev/null || fail "could not move $stage back to version $tok"
  done
done < <(jq -c '.versions[]' "$saved")
want=$(jq -r '.versions[] | select(.stages | index("AWSCURRENT")) | .value' "$saved")
[ "$(sm GetSecretValue "$(q '{SecretId: $id}')" | jq -r .SecretString)" = "$want" ] || fail "$id does not hold its saved value"
echo "$id is back with its versions; letting ESO sync"
kubectl -n sample-db annotate externalsecret db-app force-sync="$(date +%s)" --overwrite >/dev/null
synced() { [ "$(kubectl -n sample-db get externalsecret db-app -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = True ]; }
wait_for 120 "ExternalSecret db-app to sync" synced
kubectl -n sample-db annotate externalsecret db-app force-sync- >/dev/null
answers() { kubectl get --raw /api/v1/namespaces/sample-db/services/sample-db:8080/proxy/api/check >/dev/null 2>&1; }
wait_for 120 "sample-db to log in" answers
echo "Moto, the ExternalSecret and the app are as before"
