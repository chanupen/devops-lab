# External secret sync warning

An automated warning says `sample-db/db-app` cannot sync. Users see no errors yet. A platform cleanup job ran earlier. Investigate before the warning becomes an outage, without changing the database password.

## Goal

Recover credential synchronization while preserving the working credentials.

## Prerequisites

Local main lab; healthy `secrets`, `sample-db` and `obs` stacks, including Moto.
No active lab or drill. Use the [drill lifecycle](../../docs/guides/drills.md).
**Danger:** this exercise changes local emulated secret state, not AWS.
Limit is 30 minutes. Never expose secret values in reports or Git.

## Scenario

ESO reports a synchronization failure while the application still works.
Compare controller/provider state with what the running application observes.
Do not generate replacement credentials merely to clear the warning.

## What you should see

An unhealthy ExternalSecret and `ExternalSecretSyncFailed` evidence.
Existing working requests can hide a failure in the credential supply path.

## Hints

```sh
lab drills hint
```

**What you will see:** the next counted hint. The final hint can reveal the
cause; it is an optional spoiler.

## How to verify

```sh
lab drills verify
```

**What you will see:** unresolved synchronization symptoms, or recovery timing
and restoration while preserving the original credentials.

## Cleanup

```sh
lab drills abort
```

**What you will see:** restoration of saved Kubernetes and emulator state,
then an aborted result. Never force-delete secrets as a cleanup shortcut.

```sh
lab drills status
```

**What you will see after successful cleanup:** no active drill.
**Reference solution (spoiler):** [solution.sh](solution.sh). Use verify/abort
to finish and retain state if restoration fails.
