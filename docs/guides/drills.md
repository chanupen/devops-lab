# Practise an incident drill

[Docs index](../README.md) · [Labs](labs.md) · [TUI keys](tui.md#drills-keys)

A drill injects one bounded fault into the local main lab. You gather evidence,
name a cause, restore service and verify recovery. Lab saves the original state
before injection and restores it when the run ends. These instructions do not
require the engineering design document.

> **Danger:** start deliberately breaks service and can affect shared cluster
> dependencies. Use only this lab, never another cluster. Do not run injection
> or restore scripts directly. No drill is run to maintain or validate this page.
> Drills are local only; there is no cloud drill mode.

## Choose a drill

All seven currently have a 30-minute limit. The table describes symptoms,
not the cause or solution.

| Drill | Level | Page/symptom | Required stacks | Alert |
| --- | --- | --- | --- | --- |
| [bad-deploy](../../drills/bad-deploy/README.md) | Beginner | Frontend errors after a release | sample-app, obs | KubePodCrashLooping |
| [oom](../../drills/oom/README.md) | Intermediate | Backend restarts and intermittent requests | sample-app, obs | KubeContainerOOMKilled |
| [dns](../../drills/dns/README.md) | Intermediate | Service-name requests fail across apps | sample-app, obs | CoreDNSDown |
| [cert-expiry](../../drills/cert-expiry/README.md) | Intermediate | Certificate renewal warning | sample-db, obs | CertificateExpiringSoon |
| [node-loss](../../drills/node-loss/README.md) | Intermediate | Worker availability and app reachability fail | sample-app, obs | KubeNodeNotReady |
| [slow-db](../../drills/slow-db/README.md) | Advanced | Successful database requests become slow | sample-db, obs | SampleDBLatencyFastBurn |
| [secret-drift](../../drills/secret-drift/README.md) | Intermediate | ExternalSecret sync warning before user impact | secrets, sample-db, obs | ExternalSecretSyncFailed |

## Prerequisites

Start the [local lab](../getting-started/local.md) and check live health.
The default has obs but not secrets/sample-db. To prepare all seven:

**Changes the local lab; not a drill or dry run.**

```sh
lab up --mode local --stacks obs,sample-app,sample-db,secrets
```

**What you will see:** the dependency plan, then progress for missing/failed
stacks. Already-applied stacks remain. Review memory headroom first.

```sh
lab status --mode local
```

**What you will see:** applied stacks and live app/node health. Fix unhealthy
prerequisites before injection.

```sh
lab drills list
```

**What you will see:** seven entries, limits, alerts and best times. Marked
entries lack required stacks. Listing injects nothing.

```sh
lab drills
```

**What you will see:** the same read-only list, with unique recovered drills,
ended-run/recovered counts and durable best detect/recovery times. Missing best
prints `-`. Readiness marks still describe prerequisites, not completion.

Only passing verification followed by successful restoration counts a recovery.
Aborted, expired, inject-failed and restored-by-up runs count as ended runs,
not recoveries. Test runs never count. Recovery time is fault-start to successful
verify, not restoration latency. Raw history keeps 200 runs; future lifetime
counts and bests survive trimming. `history --reset [id]` clears the matching
raw history and drill summary after confirmation, but does not alter a live drill.
The [migration guide](troubleshooting.md#state-and-progress-migration) explains
what older, already-trimmed history cannot recover.

Finish/stop any active lab first. One drill can run at a time, never alongside
a started lab. CLI start/verify/abort share the mutation lock. Quit the acting
TUI before using them in another shell, or use its Drills controls.
Use the lab's kubeconfig, not a production context.

## Start and read the page

**Changes the lab: deliberate fault injection.**

```sh
lab drills start bad-deploy
```

**What you will see:** the named incident page and active timers after setup.
The manager records a snapshot before injection. Start logs are saved rather
than printed because they can reveal the fault. A start failure triggers
restoration; an error is not proof it completed.

For an unknown incident, use this **instead**, not while another run is active:

```sh
lab drills start --random
```

**What you will see:** a generic incident page. The ID/alert stay hidden until
the run ends. Random selection uses only drills whose prerequisites are up.

```sh
lab drills status
```

**What you will see:** the page, phase, timers, hints already shown and latest
verification. Status can record an observed alert, but injects no new fault.

## Timers and evidence

Time to alert, time to detect and time to recover are all measured from
injection/start, not from one another. A correct ack records detection;
a passing recovery check records recovery. A wrong ack does not stop detection.
Recovery can happen before an alert fires; history then says it did not fire.
Alert rules can wait several minutes, so absence of a page is not health proof.

Inspect symptoms, recent changes, events, logs, metrics and desired state.
Do not read `inject.sh`, `drill.yaml`'s cause, or `solution.sh` while diagnosing.
For workloads owned by Argo CD, reconcile the intended release rather than
letting a manual change fight self-heal. Never commit the injected fault to Git.
The [assistant](assistant.md) offers read-only hypotheses, not an automatic fix.

## Hints and acknowledgement

```sh
lab drills hint
```

**What you will see:** the next counted hint. **The last hint can reveal the
cause**; request it only if you accept that spoiler. Earlier hints guide the
investigation. Asking for help does not stop either timer.

```sh
lab drills ack --list
```

**What you will see:** allowed cause IDs and descriptions, not a highlighted
answer. Random runs offer suspects from all drills.

Replace `chosen-cause` with the ID supported by your evidence:

```sh
lab drills ack chosen-cause
```

**What you will see:** detection time if correct; otherwise a recorded wrong
ack and a running detect timer. Ack is not Alertmanager silencing, a repair,
or recovery verification.

## Verify and restore

**Read-only recovery check followed by restoration on success.**

```sh
lab drills verify
```

**What you will see:** a symptom-based failure until recovery, or recovery
timing and restore progress on a pass. The check has a 60-second timeout.
It validates the intended recovery, not only a superficial health signal.
Each attempt is counted.

A pass stops recovery timing, then restores the saved Kubernetes objects and
the non-Kubernetes state managed by that drill. The manager checks restored
objects before finishing. The final cause is revealed only at the end.
If restoration fails, keep the run's state and retry controlled cleanup.
A passing check alone is not permission to ignore a restore error.

## Abort, expiry and interrupted runs

**Changes the lab: restores the saved state instead of keeping your experiment.**

```sh
lab drills abort
```

**What you will see:** restore progress, an aborted result and the revealed
cause, or no active drill. Aborting is not a successful recovery score.
It is the safe exit when stuck.

```sh
lab drills status
```

**What you will see after complete cleanup:** no active drill. If it still
reports an error/restore phase, retain the snapshot and resolve it.

At the limit, a transient systemd user timer requests expiry cleanup shortly
after the deadline. It retries while another lab holds the lock; an acting
TUI restores expired runs itself. The timer does not survive a reboot.
After a crash/reboot, doctor reports the saved run and up restores it before
new work. Do not rely on a powered-off host to clean up on time.

Never delete `~/.lab/state.json` or a run directory to silence the warning.
An interrupted e2e can instead use `~/.lab/e2e/state.json`; follow doctor's
recovery guidance for that recorded run. Preserve the correct config/state.

## History and solutions

```sh
lab drills history
```

**What you will see:** completed runs, acks, hints, attempts and best times.
Time-limited/aborted runs remain distinct from recovered ones.
Do not reset history just to hide an interrupted drill.

Each incident README links a **reference solution (spoiler)** in `solution.sh`.
Reading it is optional; there is no `lab drills solution` command.
Do not run it outside the managed lifecycle, or assume it restores a snapshot.
Finish with verify or abort and confirm no active run.
