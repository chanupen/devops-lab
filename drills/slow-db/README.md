# Slow database requests

`SampleDBLatencyFastBurn` pages for sample-db. Successful `/api/check` requests are slow and its latency budget is burning quickly. Nothing was deployed. Find supporting evidence, restore latency and verify recovery.

## Goal

Separate successful-but-slow requests from availability errors and locate
where time is spent.

## Prerequisites

Local main lab; healthy `sample-db` and `obs` stacks, with steady request traffic.
No active lab or drill. Use the [drill lifecycle](../../docs/guides/drills.md).
**Danger:** this exercise affects database connectivity; limit is 30 minutes.
Do not make unrelated host networking or sudo changes.

## Scenario

Requests still succeed, but the 99% within 250 ms SLO is not met.
Compare application latency, database observations and the path between them.

## What you should see

Elevated request durations without a matching error spike, and latency
burn-rate evidence. A healthy pod and low CPU usage do not prove fast requests.

## Hints

```sh
lab drills hint
```

**What you will see:** the next counted investigation hint. The last can
reveal the cause; it is an optional spoiler.

## How to verify

```sh
lab drills verify
```

**What you will see:** whether request latency recovered, then timing and
restoration on success. Metric windows can lag current behavior.

## Cleanup

```sh
lab drills abort
```

**What you will see:** controlled database-path restoration and an aborted
result. Do not delete state after a cleanup error.

```sh
lab drills status
```

**What you will see after successful cleanup:** no active drill.
**Reference solution (spoiler):** [solution.sh](solution.sh). Finish through
verify/abort; the reference alone does not complete snapshot restoration.
