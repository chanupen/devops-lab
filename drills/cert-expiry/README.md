# Certificate renewal warning

`CertificateExpiringSoon` warns about `sample-db/sample-db-tls`. Its renewal time has passed. Recent releases look routine and nobody changed Git. Investigate before HTTPS clients lose access.

## Goal

Use certificate/controller and deployment evidence to recover renewal safely.

## Prerequisites

Local main lab; healthy `sample-db` and `obs` stacks, including their dependencies.
No active lab or drill. Use the [drill lifecycle](../../docs/guides/drills.md).
**Danger:** start changes the application's TLS behavior; limit is 30 minutes.
Never copy certificate private keys into notes or Git.

## Scenario

The certificate remains usable for now, but renewal is late. Distinguish a
renewal failure from an already-expired certificate and examine recent changes.

## What you should see

Renewal/controller evidence and a `CertificateExpiringSoon` alert after its
waiting period. Successful current HTTPS requests do not prove renewal works.

## Hints

```sh
lab drills hint
```

**What you will see:** the next counted investigation hint. The last can reveal
the cause; accept it only as a spoiler.

## How to verify

```sh
lab drills verify
```

**What you will see:** whether renewal is still overdue, or recovery timing
followed by restored application/certificate state.

## Cleanup

```sh
lab drills abort
```

**What you will see:** controlled snapshot restoration and an aborted result.
Resolve restoration errors; do not create unrelated secrets to silence checks.

```sh
lab drills status
```

**What you will see after successful cleanup:** no active drill.
**Reference solution (spoiler):** [solution.sh](solution.sh). Verify or abort
still completes the managed lifecycle.
