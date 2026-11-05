# The app's database secret is gone from Secrets Manager

The ticket queue has an automated warning: "ExternalSecret sample-db/db-app cannot sync." Users of sample-db report nothing yet. A platform cleanup job ran this morning. Find out what is wrong before it turns into an outage, fix it without changing the database password, and run `lab drills verify`.

## What happened

A cleanup job deleted secrets in Secrets Manager that it thought were unused, among them `lab/sample-db/app`, the app's database login. `delete-secret` without `--force-delete-without-recovery` does not remove a secret at once: it schedules the deletion after a recovery window (7 days here, 30 by default), and until then every read fails with `InvalidRequestException: ... marked for deletion`.

The External Secrets Operator reads the secret every minute (`refreshInterval: 1m`). Its read fails, so the ExternalSecret `db-app` turns `Ready=False` (`SecretSyncedError`). ESO keeps the Kubernetes Secret it synced last (`deletionPolicy: Retain`, the default), so the app keeps its files, and even a restart works.

Nothing has failed for users yet. These do:

- the next rotation (lab 4): the new password never reaches the cluster;
- a new namespace or cluster, or a deleted Kubernetes Secret: there is nothing to sync from, so the app cannot start;
- the end of the recovery window: the secret, with its versions, is gone for good.

## Finding it

- The alert `ExternalSecretSyncFailed` (from ESO's `externalsecret_status_condition` metric, added by the drills to the obs stack) names `sample-db/db-app`.
- `kubectl -n sample-db get externalsecret`: `db-app` is `SecretSyncedError`; `kubectl -n sample-db describe externalsecret db-app` shows the event "could not get secret data from provider".
- `aws secretsmanager describe-secret --secret-id lab/sample-db/app` shows `DeletedDate`; `get-secret-value` fails with "marked for deletion". `list-secrets` hides it unless you pass `--include-planned-deletion`.
- The ESO controller's log (`kubectl -n external-secrets logs deploy/external-secrets`) has the provider's error.

## The fix: cancel the deletion

```bash
aws secretsmanager restore-secret --secret-id lab/sample-db/app
kubectl -n sample-db annotate externalsecret db-app force-sync=$(date +%s) --overwrite   # or wait a minute
kubectl -n sample-db get externalsecret db-app    # SecretSynced
```

`restore-secret` brings back the secret as it was: the same value and the same versions (`AWSCURRENT`, and `AWSPREVIOUS` if a rotation left one). Creating it again would lose the versions, and a new password would not match the database: the app would fail at its next connection with new credentials. If someone already force-deleted it, the Kubernetes Secret still holds the current values: create the secret from those, not from a new password.

Then the lasting fixes:

- Deletion guards: an IAM policy that denies `secretsmanager:DeleteSecret` on secrets tagged as in use, or a resource policy; cleanup jobs that look at `LastAccessedDate` and ask before they delete.
- An alert on ExternalSecret sync failures (this drill's `ExternalSecretSyncFailed`), so a broken source is seen in minutes, not at the next rotation.
- Keep a recovery window (never `--force-delete-without-recovery` in automation).

## What lab puts back

lab saved the ExternalSecret and the Kubernetes Secret before the fault, and the secret's versions in Moto in the run's directory. Restore puts the Kubernetes objects back, makes the secret in Moto exactly as it was (cancels the deletion, or creates it again with the same version IDs and values, and moves `AWSCURRENT` back), lets ESO sync once and checks that the app still logs in. It never changes the database password.
