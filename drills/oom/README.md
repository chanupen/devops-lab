# Backend restarts

The frontend fails its backend calls intermittently after a backend release. The team wants to retain the release. Find the evidence for the failure, restore reliable requests through GitOps, and verify recovery.

## Goal

Connect workload termination evidence with resource behavior without discarding
the required release.

## Prerequisites

Local main lab; healthy `sample-app` and `obs` stacks. No active lab or drill.
Use the [drill lifecycle](../../docs/guides/drills.md) and the lab kubeconfig.
**Danger:** start intentionally disrupts the application; limit is 30 minutes.

## Scenario

The backend repeatedly loses availability. Compare its current behavior,
recent changes and resource observations before selecting a cause.

## What you should see

Backend restarts and intermittent frontend errors. `KubeContainerOOMKilled`
can accompany termination evidence. Do not confuse a symptom with its cause.

## Hints

```sh
lab drills hint
```

**What you will see:** the next counted hint; the last can reveal the cause.
Investigate the evidence before requesting that spoiler.

## How to verify

```sh
lab drills verify
```

**What you will see:** an unresolved symptom until the intended recovery is
complete, then timing and snapshot restoration. The required release must remain.

## Cleanup

```sh
lab drills abort
```

**What you will see:** restoration and an aborted result with the cause revealed.
Do not delete snapshot state if restoration reports an error.

```sh
lab drills status
```

**What you will see after successful cleanup:** no active drill.
**Reference solution (spoiler):** [solution.sh](solution.sh). Finish through
verify/abort, not by running the reference outside the managed lifecycle.
