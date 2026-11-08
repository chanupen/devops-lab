# Frontend errors after a release

The pager goes off: https://podinfo.localtest.me:8443 returns errors. Nobody merged anything to Git today. Find the evidence, restore service through the deployment system, and verify recovery.

## Goal

Explain a release-related outage using live evidence, then recover without
fighting GitOps ownership.

## Prerequisites

Local main lab; healthy `sample-app` and `obs` stacks. No active lab or drill.
Use the [drill lifecycle](../../docs/guides/drills.md) and the lab kubeconfig.
**Danger:** start intentionally disrupts the application; limit is 30 minutes.

## Scenario

Frontend requests fail after recent release activity. Establish what changed
and what users can no longer do. Do not read the injection or solution scripts.

## What you should see

Errors on the frontend and unhealthy workload evidence. `KubePodCrashLooping`
may fire after its rule's waiting period. A quiet alert is not proof of recovery.

## Hints

```sh
lab drills hint
```

**What you will see:** the next counted hint. The last hint can reveal the
cause; request it only if you accept the spoiler.

## How to verify

```sh
lab drills verify
```

**What you will see:** remaining user-visible symptoms, or recovery timing and
automatic snapshot restoration. A pass must reflect the deployed desired state.

## Cleanup

If stuck, use controlled restoration:

```sh
lab drills abort
```

**What you will see:** restore progress and an aborted result; the cause is
revealed. A restore error leaves work to resolve.

```sh
lab drills status
```

**What you will see after successful cleanup:** no active drill.
**Reference solution (spoiler):** [solution.sh](solution.sh). It is not a
substitute for verify/abort and the final status check.
