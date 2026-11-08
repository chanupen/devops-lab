# Service discovery failures

The frontend cannot reach its backend by name. Other applications also report errors, but the pods you inspect are Running. Restore requests and explain the evidence before verifying recovery.

## Goal

Distinguish application health from shared service-discovery health.

## Prerequisites

Local main lab; healthy `sample-app` and `obs` stacks. No active lab or drill.
Use the [drill lifecycle](../../docs/guides/drills.md) and the lab kubeconfig.
**Danger:** the incident can affect shared cluster dependencies, not only
podinfo. The limit is 30 minutes.

## Scenario

Service-name requests fail across applications. Compare independent
connectivity observations; a Running pod is not necessarily reachable.

## What you should see

Frontend/backend communication failures and possible GitOps comparison errors.
The named alert is `CoreDNSDown`. Use its evidence, not the alert name alone,
to establish what happened.

## Hints

```sh
lab drills hint
```

**What you will see:** the next counted hint. The final hint can reveal the
cause, so it is an optional spoiler.

## How to verify

```sh
lab drills verify
```

**What you will see:** whether user-visible name-based communication recovered,
then timing and restoration on success.

## Cleanup

```sh
lab drills abort
```

**What you will see:** controlled restoration, including shared objects saved
before injection, and an aborted result.

```sh
lab drills status
```

**What you will see after successful cleanup:** no active drill.
**Reference solution (spoiler):** [solution.sh](solution.sh). Do not bypass the
manager or discard restore state.
