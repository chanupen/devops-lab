#!/usr/bin/env bash
# Deletes the app's secret in Secrets Manager (Moto) the way a cleanup
# job would: with a recovery window, so reads fail but the secret can be
# restored. The secret's versions and values are saved first, in the run's
# directory, so restore can put Moto back exactly.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
id=lab/sample-db/app
mkdir -p "$DRILL_STATE"
desc=$(sm DescribeSecret "$(jq -cn --arg id "$id" '{SecretId: $id}')") || fail "Secrets Manager (Moto) has no $id: is the secrets stack up? lab status"
[ "$(jq -r '.DeletedDate // empty' <<<"$desc")" = "" ] || fail "$id is already scheduled for deletion"
versions='[]'
for v in $(sm ListSecretVersionIds "$(jq -cn --arg id "$id" '{SecretId: $id}')" | jq -r '.Versions[].VersionId'); do
  val=$(sm GetSecretValue "$(jq -cn --arg id "$id" --arg v "$v" '{SecretId: $id, VersionId: $v}')")
  versions=$(jq -c --argjson val "$val" '. + [{id: $val.VersionId, value: $val.SecretString, stages: $val.VersionStages, created: $val.CreatedDate}]' <<<"$versions")
done
jq -n --arg id "$id" --argjson v "$versions" '{secret: $id, versions: ($v | sort_by(.created))}' >"$DRILL_STATE/moto.json"
echo "saved $(jq length <<<"$versions") version(s) of $id"
sm DeleteSecret "$(jq -cn --arg id "$id" '{SecretId: $id, RecoveryWindowInDays: 7}')" >/dev/null
echo "deleted $id with a 7-day recovery window, as a cleanup job would"
