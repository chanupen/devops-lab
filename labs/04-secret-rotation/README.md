# Lab 4: Rotate credentials without restarting the app

Prove that new database credentials reach a running process without failed traffic.

## Goal

Use alternating database users, Secrets Manager and ESO to rotate credentials.
Allow 40 minutes; intermediate level.

## Prerequisites

Local or cloud main lab, healthy `secrets` and `sample-db`; rootless Podman,
kubectl and jq. Start lab 4 and select its exports with
[the lifecycle guide](../../docs/guides/labs.md). No other active lab/drill.
Work in `lab04-app` and `lab04-traffic`, with private files in `~/lab04`.
The exercise deploys its own app/database, not the stack's running database.
Append `--mode cloud` to every labs hint/verify/reset/stop command below for
a cloud session; the examples otherwise use the configured mode.

Local exports target Moto on `127.0.0.1:4566` with dummy credentials.
**Billed — approval required in cloud:** Secrets Manager operations are real.
Use `ap-south-1`, no emulator endpoint, and `LAB_AWS_TAGS` on both created secrets.
ESO uses EKS Pod Identity. Keep passwords/export files out of Git and shell history.

Define the pinned AWS CLI wrapper after selecting the lab environment:

```sh
aws() { podman run --rm --network host -e 'AWS_*' docker.io/amazon/aws-cli:2.38.3@sha256:efe32e825cf203652eaca3e7e94679580274e7d567c39270aaa36a5144737108 "$@"; }
```

**What you will see:** no output until invoked. Calls use your selected endpoint,
region and credentials. Do not use real AWS credentials for the local exercise.

## Steps

1. [Deploy and inspect](#step-1-deploy-the-app-and-read-its-setup).
2. [Count traffic failures](#step-2-start-traffic-that-counts-failures).
3. [Add the next login](#step-3-add-a-second-login-role).
4. [Update the source secret](#step-4-update-the-secret-in-secrets-manager).
5. [Observe a live switch](#step-5-watch-the-app-switch-without-a-restart).
6. [Retire old access](#step-6-retire-the-old-role-and-prove-it).

## What you should see

Credentials move from Secrets Manager through ESO to a whole Secret volume,
then the app's connection pool. ESO refreshes hourly; kubelet volume updates
usually take about a minute; the app rereads every two seconds.
Environment variables and subPath mounts do not receive these live updates.
The goal is a stable pod UID, no restarts and zero failed requests.

Postgres role `app` owns privileges without login. `app_v1` and `app_v2` are
login members; overlapping credentials let the app switch safely.
The superuser has separate credentials and is not rotated here.

## Hints

```sh
lab labs hint
```

**What you will see:** the next hint for the current step.

## How to verify

```sh
lab labs verify
```

**What you will see:** a read-only check of the app, database and secret state;
a pass advances progress. Output can contain usernames, never share password values.
**Reference solutions (spoilers):** [solution scripts](solution/).

## Cleanup

**Destructive — cloud deletion needs approval and has no recovery window:**

```sh
lab labs reset
```

**What you will see:** removal of `lab04-app`, `lab04-traffic` and Secrets Manager
secrets `lab04/db/admin` and `lab04/db/app`. Private `~/lab04` files remain.

```sh
lab labs stop
```

**What you will see:** the session ends. Use `--mode cloud` in cloud.
Close the credential-bearing shell; review retained private files separately.
Cloud stop does not end EKS or other resource billing.

## Step 1: Deploy the app and read its setup

Create Secrets Manager JSON secrets with fields `username` and `password`: `lab04/db/admin` for `postgres`, and `lab04/db/app` for `app_v1`. Use random private passwords. Deploy the files in `app/` except `traffic.yaml`, replacing `SAMPLE_DB_IMAGE` with the sample-db stack's digest-pinned image. Record the running app pod UID as Deployment annotation `lab.devops/pod-uid`.

### Hints
- Keep secret input in private files rather than literal command-line passwords.
- Inspect the provided manifests for the mount path and ESO refresh interval.
- Annotating Deployment metadata differs from modifying its pod template.

### Expected result
Both ExternalSecrets are Ready; Postgres and the app run. `/api/check` reports user `app_v1` and the original pod UID is recorded. ESO refresh interval is 1h.

## Step 2: Start traffic that counts failures

Start the provided `app/traffic.yaml` Job after initial health is established. Keep it running through old-credential retirement; it calls `/api/check` about four times/second and stops automatically after 45 minutes.

### Hints
- Follow the traffic Job's progress in another terminal.
- If traffic already fails, fix baseline health before measuring rotation.

### Expected result
Traffic progress counts increase with `failed=0`.

## Step 3: Add a second login role

Create ordinary login `app_v2` with a new random password and membership in role `app`, without superuser privileges. Leave `app_v1` unchanged and keep the new password privately for the next step.

### Hints
- Use the superuser's local socket for administration, not an app login.
- Membership supplies privileges; TCP password authentication proves the login works.

### Expected result
Both logins can authenticate with the shared privileges; the app still uses `app_v1`.

## Step 4: Update the secret in Secrets Manager

Store `app_v2` credentials as the new version of existing secret `lab04/db/app`. Do not delete/recreate the secret or retire the old role yet.

### Hints
- Version staging preserves the former value as AWSPREVIOUS.
- Keep database creation, secret publication and client switching in safe order.

### Expected result
AWSCURRENT holds `app_v2`; AWSPREVIOUS still holds `app_v1`. Before ESO's next sync, the application still uses the old credentials.

## Step 5: Watch the app switch without a restart

Request an early ESO refresh for this exercise rather than waiting an hour. Observe the updated Secret volume, new app connection pool and original pod. Do not restart the app to make the new credentials appear.

### Hints
- Inspect the ExternalSecret controller's supported refresh trigger.
- Compare the saved UID and restart count; use the app's reload metrics/logs.
- In normal operation, allow a full refresh interval plus propagation before retiring credentials. More frequent provider calls can incur cloud charges.

### Expected result
`/api/check` reports `app_v2`; UID is unchanged, restart count is zero and `sampledb_credential_reloads_total` is 1. Traffic still reports zero failures.

## Step 6: Retire the old role and prove it

Check that old sessions are gone, then retire `app_v1` by dropping it or removing login permission. Prove that its previous password no longer authenticates over the Service's TCP connection. Keep traffic running during the check.

### Hints
- Revoking future login does not end existing database sessions.
- A local trusted socket does not test password rejection.
- Evaluate both authentication and requests, not only role configuration.

### Expected result
No old sessions remain; previous credentials are rejected and the app uses `app_v2`. The traffic Job has covered the rotation and still reports `failed=0`.
