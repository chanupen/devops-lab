# Worker availability incident

Frontend requests fail for some users, then for more of them. Nobody deployed anything. Restore service and explain why the platform did not absorb the loss of availability.

## Goal

Relate node and workload availability, recovery timing and monitoring placement.

## Prerequisites

Local main lab; healthy `sample-app` and `obs` stacks and kind workers.
No active lab or drill. Use the [drill lifecycle](../../docs/guides/drills.md).
**Danger:** the incident can affect workloads sharing a worker, including
monitoring. Limit is 30 minutes; never act on unrelated host containers.

## Scenario

Application reachability worsens without a release. Compare node conditions,
workload placement and the availability of the monitoring path.

## What you should see

A worker availability problem, unhealthy workloads and eventually
`KubeNodeNotReady` if monitoring can report it. Recovery/alert timing depends
on placement; do not assume a fixed number of seconds.

## Hints

```sh
lab drills hint
```

**What you will see:** the next counted hint. The last can reveal the cause.

## How to verify

```sh
lab drills verify
```

**What you will see:** unresolved availability symptoms, or recovery timing
and restoration checks for the lab's nodes and applications.

## Cleanup

```sh
lab drills abort
```

**What you will see:** managed node/workload restoration and an aborted result.
Do not remove containers or node data as a shortcut.

```sh
lab drills status
```

**What you will see after successful cleanup:** no active drill.
**Reference solution (spoiler):** [solution.sh](solution.sh). Placement can
change during recovery; complete verify/abort and the final status check.
