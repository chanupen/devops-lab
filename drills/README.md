# Drills

For the seven-drill catalog and learner lifecycle, read
[the drills guide](../docs/guides/drills.md). Individual pages describe symptoms;
complete fixes stay in labelled solution scripts. This page is the authoring
and safety contract, not an answer key.

## Format

```text
drills/<id>/
  drill.yaml     catalog, prerequisites, limits, hints, cause choices, snapshot
  README.md      incident intro and seven learner lifecycle sections
  inject.sh      puts the fault in
  verify.sh      read-only recovery check; last line explains failure
  restore.sh     idempotent external-state restore and health wait
  solution.sh    reference fix (spoiler)
drills/lib.sh    shared helpers
```

Every README has Goal, Prerequisites, Scenario, What you should see, Hints,
How to verify and Cleanup. Keep a nonempty, symptom-only incident introduction
between the title and first H2: that text becomes `Drill.Page`.
Random runs instead display a generic page and hide the ID/alert.
Do not add injection mechanics, the correct cause or repair commands to learner
text. The last runtime hint can reveal the cause and is labelled as a spoiler.

| Definition field | Contract |
| --- | --- |
| id | Same words-with-dashes as the directory |
| title, level | Catalog text |
| stacks | Main-lab prerequisites; random selection uses eligible drills |
| timeLimit | Cleanup deadline; default 30m |
| alert | Prometheus alert name/labels |
| hints | Ordered investigation hints; last can reveal the cause |
| cause, causes | Real cause ID and at least three plausible suspect descriptions |
| spoilers | Words the pre-recovery verify message must not disclose |
| touches | Namespaces allowed to change and compared by e2e |
| snapshot | Kubernetes objects saved before injection and checked after restore |

## Safety and execution

One local drill at a time, never alongside a started lab. Start, verify and
abort take the shared mutation lock; the TUI serializes its own actions.
The manager snapshots before injection. Snapshot restore recreates/deletes
objects as required, runs the external-state restore and compares saved state.
Failure retains the active run/error for retry, never silently discards it.
A failed inject triggers restoration too.

Expiry uses a transient systemd user timer with retries while the lock is busy.
The acting TUI handles expiry itself. Timers do not survive reboot: doctor
reports saved runs and up restores them before new work. State and run directories
can be sensitive; never commit them or delete them to hide a fault.

Workload faults respect GitOps ownership without committing faults to Git.
Shared infrastructure exceptions must remain bounded, declared and recoverable.
Restore must cover Kubernetes and non-Kubernetes state regardless of what the
learner changed. Keep the API server reachable even during dependency failures.

Scripts receive `KUBECONFIG`, `DRILL_ID`, `DRILL_DIR`, `DRILL_RUN`, `DRILL_STATE`,
registry exports and local Moto variables. `DRILL_STATE` holds external restore
data in `~/.lab/drills/<run>/`; the manager removes it after completion.
Inject/restore have ten-minute timeouts; verify has 60 seconds.
Verify is read-only/idempotent and describes unresolved symptoms before validating
the intended recovery. Restore is idempotent and checks health.
Images stay digest-pinned and run through rootless Podman.

## Timing and alerts

Alert, detection and recovery times all start at injection. Wrong acks and
hint/verify attempts are recorded. Correct ack stops detection; successful
verify records recovery, then restores original state. Abort/expiry are not
successful recovery scores.

Alerts come from the obs stack and the sample-db SLO rules. Their waiting
periods can exceed fast human recovery. If an alert did not fire before recovery,
history records that honestly. Do not promise a fixed alert/launch latency.

## Contributor validation

**No live faults:** unit tests simulate timers, state, acks and restore.

```sh
go test ./internal/drills/
```

**What you will see:** package test results, with no injected cluster fault.

> **Danger — live fault injection:** the documented e2e suite runs every drill
> on the main lab. Use it only for approved behavior changes, never a reader
> documentation check. It holds the lock and uses separate e2e state; an
> interruption can still leave a real fault requiring controlled restoration.

```sh
LAB_E2E_MAIN_KUBECONFIG=~/.lab/kubeconfig go test -tags e2e -run E2EDrills -v -timeout 60m ./internal/drills/
```

**What you will see in an approved run:** inject/failing symptom check, alert
observation, reference recovery and restore checks, then an abort path.
Interrupted runs remain in `~/.lab/e2e/state.json`; doctor reports them and
up restores them. `LAB_DRILLS_TEST=1` keeps maintainer runs out of learner history;
it does not make fault injection safe or read-only.
