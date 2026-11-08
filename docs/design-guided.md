# Guided first run and daily use

**Status:** design approved and merged in #52. B1 implemented sections 1 and 2 (#53).
B1.1 fixes accepted-warning recommendations. Sections 3 and 4 remain future
work; B2 starts only after B1.1 merges.
Start/next examples below are fixture output, not live-run evidence.

[Engineering reference](design.md) · [Documentation rules](contributing-docs.md)

## Scope and existing behavior

Make local startup understandable. Give one useful next action. Keep completion
records after exercise cleanup. Print actionable doctor fixes without running them.
Do not add cloud startup, a new TUI screen, inference or an automatic installer.
The repository stays private.

Existing code is the starting point:

| Piece | Existing owner | Required extension |
| --- | --- | --- |
| Plans and execution | [internal/engine](../internal/engine/plan.go) | Reuse dependency ordering, skips, retries, revision handling and execution |
| CLI apply orchestration | [cmd/lab/up.go](../cmd/lab/up.go) | Share preparation/apply with start; do not shell out to `lab up` |
| Provider support and locks | [cmd/lab/common.go](../cmd/lab/common.go) | Reuse support lookup and the non-blocking lab lock |
| Doctor results | [internal/doctor](../internal/doctor/doctor.go) | Share collection; expose structured memory and, later, fix commands |
| State | [internal/state/state.go](../internal/state/state.go) | Migrate existing `version: 1` to version 2, not a second version field |
| Lab progress | [internal/labs/manager.go](../internal/labs/manager.go) | Extend completion recording and attempt timing |
| Drill progress | [internal/drills/manager.go](../internal/drills/manager.go) | Retain best times beyond the bounded run history |
| Home | [internal/tui/home.go](../internal/tui/home.go) | Add one cached progress line, including on empty-state Home |

Local-only command refusal is a product boundary, not a new provider fork.
`start` and `next` reject a cloud configuration before constructing a provider
or running probes. They never silently override a cloud configuration.
Provider work remains in `internal/provider/`; existing cloud commands and
TTL/cleanup safeguards remain unchanged.

## 1. `lab start`

**Changes the local lab after confirmation.** This is an onboarding wrapper
around the same apply path as `up`. It is not another engine.

| Flag | Contract |
| --- | --- |
| `--profile minimal\|standard\|full` | Override automatic selection for this invocation |
| `--yes` | Confirm the displayed startup plan without a terminal prompt |
| `--dry-run` | Run read-only preflight and print the plan; do not apply |
| `--config`, `--state` | Use the existing configuration/state path conventions |

There is no `--mode` flag. TTL, prune, refresh and force options are not part
of start.
`--yes` never bypasses readiness, compatibility or active-exercise checks.
`--dry-run --yes` remains read-only and never prompts.

### Profile selection and doctor

Doctor owns the `/proc/meminfo` parser and profile thresholds. Expose typed
total/available byte values and the existing threshold lookup. Do not parse
formatted doctor text or copy its threshold map into the CLI.

Automatic selection picks the largest profile whose threshold fits
`MemAvailable`: full at 19 GiB, standard at 12 GiB, minimal at 4 GiB.
These are the current doctor thresholds, not measured incremental allocations.
Ignore swap. Missing or invalid memory data blocks startup; do not guess.

An explicit profile changes the selection, not the memory gate. Below its
available-memory threshold, start refuses with exit 1, even though ordinary
doctor treats low available memory as a warning. Below 4 GiB, automatic start
also refuses. This conservative first-run policy applies even to a repeat start;
`next` remains usable for daily reports.

Preserve `extraStacks` and show them in the plan. Never silently drop an extra
stack to fit a budget. Profile thresholds are estimates; extras and other
workloads can exceed them. Say this before confirmation.
Do not write the selected profile back to `lab.yaml`.
Later plain `up` still uses the configured profile; start repeats its selection.

Run doctor as a shared collection operation, not a subprocess. Its memory
preflight selects the profile; remaining checks then run for that selection.
Separate requested-stack host prerequisites from deployed-stack health checks:
a requested AI stack needs GPU readiness, but cannot need healthy AI pods
before its first deployment. Keep existing doctor defaults compatible.
Do not run the same expensive probe twice just to choose a profile.

The existing local registry doctor probe is not strictly read-only:
`CheckLabRegistry` POSTs a test upload and DELETEs it afterward.
Preflight and next must not call it. Add an observational provider variant
that reads registry availability, loopback bindings and mirror-file presence.
Explicitly label write permission as untested, not passing. The same variant
is used by print-only fix reports. Keep the ordinary doctor's existing probe
compatible; do not change unrelated doctor semantics in this design.

B1 adds local `doctor --read-only`, `--config` and `--state` for replayable
diagnostic suggestions. The observational registry probe uses GET and rejects
redirects. A failed observational read blocks guided readiness, even where
ordinary doctor retains its skip status.

Failures block apply. Other warnings are shown before confirmation.
Unsupported stacks are still skipped by engine planning, not installed or
treated as failed stacks. Darwin prints the existing Linux-host guidance.

### Ordering and consent

1. Validate flags, local configuration and Linux execution. Read state and the
   adjacent e2e drill state without writing or hiding parse errors.
2. Run doctor preflight, choose the profile and collect the remaining results.
   Refuse an active lab or drill, including an interrupted e2e drill. Do not
   restore it implicitly; direct the operator to its existing lifecycle commands.
3. Build the preview with `engine.PlanUp`, the selected in-memory config and
   the existing provider support lookup. Show keeps, skips, retries, warnings
   and any proposed restart of the main nodes/image cache.
4. For dry-run, stop here. Otherwise ask `Start this local plan? [y/N]`.
   Noninteractive input needs `--yes`. Decline, EOF and interruption do not apply.
5. Take the existing non-blocking shared lab lock. Reload inputs and revalidate
   consent against the current state/configuration. If the request changed,
   stop and require a fresh invocation rather than applying an unseen plan.
6. Enter the shared up orchestration. Retain version gates, stopped-container
   recovery, staleness checks, chart prefetch, engine execution, state logs and
   app output. Recheck the execution plan before stack application. If live
   discovery changes it, print the change and stop for fresh consent.
7. Print next steps only after success. Release the lock on every exit path.

Today `up` restores drills and starts stopped containers before displaying its
plan. Start must not invoke that path during preflight. Extract a read-only
preparation boundary and a consented execution boundary; do not copy its RunE.
Readonly provider observations belong in provider helpers, not a second CLI
implementation of Podman behavior.

A dry-run is a state-derived preview, not a guarantee of future live readiness.
Unavailable live checks are labelled, not reported as passing. After consent,
nodes/cache may have restarted before a later gate or download fails; report
that fact. Do not roll back pre-existing containers or hide partial failures.
Already-deployed stacks outside the selected profile stay deployed, as with up.

### Fixture output

**Read-only preview.**

```text
$ lab start --profile minimal --dry-run
● memory        8.0 GiB available of 24.0 GiB, minimal needs ~4 GiB
○ lab registry  Zot is not there yet (lab up makes it); write permission untested (read-only probe)

1 ok, 0 warn, 0 fail, 1 skipped

Profile: minimal (explicit); 8.0 GiB available; threshold 4 GiB
Extra stacks: obs; memory estimates are not a hard capacity guarantee
local mode, minimal + obs profile, Kubernetes 1.36, gitops revision main

  + registry    not deployed
  + cluster     not deployed
  + gitops      not deployed
  + core        not deployed
  + sample-app  not deployed
  + obs         not deployed

6 to apply, 0 to destroy, 0 unchanged
Dry run: no apply, restart, chart download or state write.
```

**What you will see:** doctor results and the existing plan format, followed by
an explicit no-change statement. Golden fixtures freeze memory, state and probes.

After a successful confirmed run, append:

```text
Next steps:
  lab status
  lab next
  lab
```

**What you will see:** commands to inspect health, get a recommendation and open
the TUI. Nothing launches automatically. Explain that a TUI can hold the lab lock.
Include resolved configuration/state flags when non-default paths were selected.

### Exit codes, state, tests and docs

| Exit | Meaning |
| --- | --- |
| 0 | Successful apply/no-op, or a ready dry-run |
| 1 | Readiness/memory failure, invalid input, declined/missing confirmation, busy lock, stale consent, I/O or apply failure |
| 2 | Unsupported execution host or cloud configuration |

Dry-run changes no state, config, kubeconfig, containers, charts or lock files.
Confirmed runs use the existing atomic store and operation logs.
Record the replayable `lab start --profile <selected>` invocation with its paths.
Do not invent an executable `lab up --profile` command: up has no such flag.
Cancellation during apply preserves the existing partial-state/error behavior.

Table-driven tests cover threshold boundaries, invalid memory, explicit profiles,
extras, skips, retries, cloud/Darwin refusal, every confirmation outcome, lock
contention, active exercises, changed consent, no-op and apply failures.
Assert zero provider mutations/writes/downloads in dry-run, refusal and decline.
Use fake memory, probes, providers, clocks and temporary state paths.
Add golden stdout/stderr fixtures for ready and blocked `start --dry-run`.

Document start in the local quickstart, CLI reference and profiles guide.
Explain ephemeral selection, memory gates, extras and the acting-TUI lock.
Cloud quickstart only notes that start is local-only; no cloud sequence changes.

## 2. `lab next`

**Read-only recommendation.** Print one suggested step and its exact command.
Never execute it. Support `--json`, `--config` and `--state`.
Initial scope is local Linux, like start.

Load state, catalogs and fresh bounded doctor results through the shared
collector. Use resolved paths consistently; do not accidentally probe the default
lab when a custom state was selected. Add doctor path flags in the first
implementation PR if needed for replayable diagnostic suggestions.
Do not persist a doctor cache or recommendation. Do not call drill status,
CheckAlert, hints, solution display or assistant commands to gather evidence.

Recommendations are deterministic. Choose one row, in this priority order:

| Condition | Suggestion | Printed command effect |
| --- | --- | --- |
| Interrupted/restoring/expired drill, including e2e state | Explicit recovery with `lab drills abort` and the correct state path | Changes the lab |
| Running drill | Inspect symptoms/timers with `lab drills status`; hide a random drill's identity | May record drill observations |
| Failed doctor check | Inspect its instructions with `lab doctor --read-only --profile <configured>` | Read-only |
| Active lab, including a completed one awaiting cleanup | `lab labs status` | Read-only; shows the existing cleanup guidance |
| Stopped main nodes/cache or registry | `lab start --profile <configured>` | Changes the lab after confirmation |
| Empty state or engine plan needs apply/retry | `lab start --profile <configured>` | Changes the lab after confirmation |
| Remaining actionable doctor warning | Inspect it with `lab doctor --read-only --profile <configured>` | Read-only |
| Ready lab, eligible incomplete exercise | Start the lowest-numbered eligible lab; after those, an eligible random drill | Changes the lab; never injects automatically |
| No eligible incomplete exercise | `lab labs list` to review progress/replay options | Read-only |

Use engine planning for missing/retry work, not a duplicated dependency walker.
Eligibility uses catalog modes, prerequisites and tool availability. Missing
prerequisites lead to a review/list suggestion, not automatic stack installation.
A failed probe never counts as ready. Within doctor failures/warnings, use the
existing check order. Do not recommend unsupported stacks or disclose solutions.
Warnings with a fix marker of `allowed` or `accepted`, alone or before a colon
and explanation, do not trigger this row. Matching ignores case and surrounding
whitespace. Doctor still displays them; failures are never exempted.

A completed run in the progress ledger counts for exercise selection even if
scratch cleanup removed its current session. Recommendations are advisory:
re-checks and confirmations remain the responsibility of the printed command.
Render exact supported commands, quote selected paths, and never add `--yes`.
Before fix support lands, do not suggest a nonexistent `doctor --fix` flag.

Until the B2 ledger exists, B1 reads version-1 session completion and bounded
drill history only. Reset/scratch cleanup can make a lab eligible again.
The existing `--random` selector can replay a completed eligible drill; next
does not add a new selector or expose its choice.

### Fixture output

```text
$ lab next
Next: Bring up the configured local profile.
Command: lab start --profile minimal
Effect: changes-lab; not executed.
```

**What you will see:** one reason, one command and its effect. This example uses
empty state and passing doctor fixtures, not a live readiness claim.

```text
$ lab next --json
{
  "schemaVersion": 1,
  "kind": "start",
  "reason": "Bring up the configured local profile.",
  "command": "lab start --profile minimal",
  "effect": "changes-lab",
  "requiresSudo": false
}
```

**What you will see:** one JSON object, with no banners or probe output on stdout.
JSON schema version 1 is independent of the state schema. B1 retains state
version 1; version 2 is planned in B2.
Use effect values `read-only`, `writes-state`, `changes-lab` and `host-change`.

Exit 0 means a valid suggestion, even if doctor found a blocking failure.
It does not mean the lab is healthy. Exit 1 means invalid arguments or unreadable/
unsupported state, configuration, catalog or report data. Exit 2 means cloud
configuration or a non-Linux host. Fatal errors go to stderr, not partial JSON.

State changes: none, including no on-disk migration, directories, lock acquisition,
alert recording or telemetry. Read-only next must work while another lab acts.
New probes are observational; external network reads may still occur in doctor.

Table-driven tests cover every priority and overlapping conditions, unknown
catalog IDs, unavailable prerequisites, random-drill redaction, missing state,
corrupt/future-version state, failed/timed-out probes and quoted custom paths.
Add text/JSON goldens and assert unchanged state bytes/mtime and no side effects.
Document next in the local daily-use tour, CLI reference and troubleshooting.

## 3. Durable exercise progress

The local ledger stays in `~/.lab/state.json`; respect explicit state paths.
Existing cloud lab progress stays in its own `~/.lab/cloud/state.json`.
Do not merge modes or touch AWS to migrate a file.

Version 2 adds a compact `progress` object with lab/drill maps keyed by catalog ID.
Keep current `labs`, `activeLab`, `drills`, `activeDrill`, stacks, revision and ops.
Current session progress and historical completion are different concepts.

| Record | Fields and meaning |
| --- | --- |
| Lab summary | Completion count, last completion UTC timestamp, optional best active-attempt duration |
| Drill summary | Ended-run count, recovered count, last completion UTC timestamp, optional best alert/detect/recovery durations |
| Lab attempt timing | Elapsed active time and a known/unknown marker; reuse existing session and scratch uptime accounting |

Persist durations as explicitly named nanosecond integer fields; timestamps use
RFC 3339. Missing best duration means unknown, not a zero-second achievement.
Count completions only on the first successful final-step transition for a lab.
Re-verifying that step does not award another completion.
Timing tracks one attempt across pause/resume; successful reset begins a new
attempt. Do not change the meaning of the existing displayed `Spent`/`Session`.
Keep the extra timing counter folded at lifecycle boundaries using the existing
`TimeSpent` logic, rather than another uptime implementation.

For drills, count recovery only when verify passes **and** restoration succeeds,
at the existing `EndDrill` transaction. Best recovery is Started-to-Recovered,
matching today's best-time definition, not restoration latency.
Aborted, expired, inject-failed and restored-by-up runs are not completions.
Test runs never enter learner totals. Reject invalid/negative best times.

Completion updates and lifecycle changes occur in the same `Store.Update`.
Move/reuse the existing best-time fold so migration, CLI and TUI do not each
implement it. Keep active snapshots and restore errors intact on failed writes.
Trim raw history only after updating the durable summary. Its 200-run cap must
not erase a later all-time best. Scratch stop and lab reset retain completion
summaries, while preserving their current resource/session cleanup.
Explicit `drills history --reset [id]` clears matching drill summaries too,
consistent with its current best-time reset. There is no new lab-progress erase
command in this scope.

### Migration

Support exactly version 1 to 2 and idempotent loading of version 2.
Reject malformed, unversioned and future-version files without changing them.
Missing state is an empty version-2 state in memory; reading does not create it.

`Store.Load` migrates in memory only. The next successful existing state mutation
persists version 2 under the store lock, with atomic rename/fsync and private
permissions. Read-only views do not write an upgraded file or take the lab lock.

Seed one known lab completion from each nonzero legacy `Completed` timestamp.
Do not invent completion counts already lost when scratch state was deleted.
Legacy lab timing may have been reset or resumed after completion: seed its best
as unknown. Mark migrated in-progress attempt timing unknown until a fresh
attempt/reset; do not report just its post-upgrade work as a full-attempt best.

Fold retained non-test drill history using the existing recovery/time semantics.
Counts and bests from runs already trimmed or reset cannot be recovered.
State/documentation must describe this historical limitation, not claim complete
pre-upgrade totals. Future completions remain durable.
Preserve every recovery field and unknown catalog ID during migration.
Older binaries reject version 2; do not recommend changing the number back or
discarding fields as a downgrade procedure.

### Display, exit codes, tests and docs

Bare `lab labs` and `lab drills` become aliases for their existing list views,
not automatic starters. `--help` still shows command help. Add matching path
flags without changing explicit list/status semantics.
Lists distinguish current step/readiness from lifetime completions and bests.
An off scratch lab can have a completed historical run.

Abbreviated proposed output, with fixture catalogs:

```text
$ lab labs
Progress: 1/5 eligible local labs completed
ID                   CURRENT      COMPLETIONS  BEST
01-argocd-bootstrap   not started  1            18m

$ lab drills
Progress: 2/7 local drills recovered
ID          RUNS  RECOVERED  BEST DETECT  BEST RECOVER
bad-deploy  3     2          1m20s        4m10s
```

**What you will see:** ordinary catalogs plus durable progress. A missing best
prints `-`. Denominators use the current mode's catalog, not hard-coded counts.
Only one row from each proposed catalog is shown here.

Home adds exactly one line, before its empty-state return:

```text
progress  labs 1/5 complete | drills 2/7 recovered
```

**What you will see:** unique completed exercises, not the number of attempts.
Unavailable drills display `n/a`, not a misleading zero. Catalog eligibility is
supplied through existing mode/provider boundaries.

List aliases exit 0 on a valid view, including empty progress, and 1 on input,
load or output errors. Existing exercise action exit codes remain unchanged.
Lists/Home write nothing. Existing successful verify/recovery/reset actions own
the progress mutations; no new lock or background autosave is added.

Home reads only preloaded state/catalogs. No doctor, Podman, Kubernetes, clock
probe or migration write runs in View or the key loop. File refresh/background
messages update cached state through the existing path.
Keep first frame under 100 ms and retain redraw/log-buffer limits.

Table-driven tests cover migration, idempotence, corruption/future versions,
atomic-update failure, concurrent updates, completion deduplication, stop/reset/
resume timing, legacy unknown timing, history trimming/reset and excluded test
runs. CLI/TUI summaries must agree from the same fixtures.
Update Home and list goldens, including empty/narrow views and read-only TUI.
Run the existing first-frame test with slow/failing background fakes and record
timing; no new live exercise e2e run is needed for this change.

Document persistent versus current progress in labs/drills and TUI guides,
the migration/downgrade constraint in troubleshooting, and schema ownership in
the engineering reference.

## 4. `lab doctor --fix`

**Print only.** Use the same check catalog and thresholds as ordinary doctor,
with the observational registry variant described above, then print copyable
commands for every failed or warned result. Label the unperformed write probe.
Never execute remediation,
including with `--yes` or noninteractive input. No installer or subprocess runner
is added to the remediation path.

Current `Result.Fix` often contains prose, alternatives, placeholders or empty
text. Do not split it into shell commands or infer sudo from a substring.
Add typed remediation metadata next to each check: command, requires-sudo flag,
effect, explanation and whether it is a diagnostic/manual prerequisite.
Keep the legacy hint and ordinary doctor output compatible.

Every fail/warn must have an actionable entry, in check order. Prefer an exact
repair command when safe and fully known. Otherwise explicitly say
`manual action required` and give an exact read-only inspection command.
There is no universal shell repair for low RAM, unreadable data or unknown
service ownership; do not falsely label a diagnostic command as a repair.
Resolve names/paths from validated inputs. Never print `<app>`, `...` or a
guessed destructive command as copyable remediation.

Mark commands requiring privilege as `sudo required`. Also mark host changes,
lab changes, destructive cleanup and existing billed cloud actions with their
approval requirements. Printing a command is not consent to run it.
Keep existing cloud doctor behavior; this feature adds no cloud workflow.
Cloud remediation data belongs with existing provider probes; test it with fakes.

### Proposed output

```text
$ lab doctor --fix
Fix commands (PRINT ONLY; nothing below was executed):
FAIL podman: not found
  sudo required; changes host; approval required
  sudo dnf install podman
WARN memory: insufficient available memory
  manual action required: close memory-heavy applications
  read-only inspection: free -h
```

**What you will see:** per-check commands and safety labels. The second command
inspects memory; it does not claim to free it. Re-run doctor after manual fixes.

Allow `--fix --json`: retain profile/ok/results and attach structured remediation
entries without human banners on stdout. For all-healthy reports, text says
`No fix commands needed`. Stopped/skip results retain their existing hints;
they are not promoted to failed/warned checks just to produce fixes.

Exit 0 means no failed checks, including warnings with printed fixes.
Exit 1 means a failed check, invalid flags or report/output failure.
Exit 2 keeps the existing unsupported-host outcome. `--fix` never turns a
diagnostic failure into success. State changes: none, as with ordinary doctor;
no remediation, telemetry, migration write or lock is performed.

Table-driven tests cover every fail/warn remediation branch, sudo labels,
manual/diagnostic distinctions, exact quoting, compound commands, empty/healthy
reports, JSON and unchanged exit codes. Assert no remediation runner calls or
filesystem writes. All probe/installer/privileged-command tests use fakes.

Document print-only fixes in local quickstart and troubleshooting, regenerate
CLI help, and describe JSON/sudo labels. Once implemented, next's doctor-failure/
warning suggestions can use `lab doctor --fix` with the selected profile/paths.
Update their golden fixtures in the same PR.

## PR boundaries and approval gates

| PR | Contents | Gate |
| --- | --- | --- |
| Step A: this design | Only `docs/design-guided.md`; no code or other documentation changes | Open draft, send link, stop for approval |
| Step B1: guided local use | Sections 1–2 only: start, next, shared doctor input/memory, observational registry/path flags, shared up preparation/apply, focused tests/goldens and docs | Design approved; open draft, stop, do not merge |
| Step B2: durable progress | Section 3: completion/best times, version-2 migration, exercise summaries and cached Home line | Only after B1.1 merges and owner authorises it; separate draft |
| Step B3: print-only fixes | Section 4: typed remediation, doctor fix text/JSON, exact-command/sudo coverage and next golden updates | Only after B1 merges and owner authorises it; separate draft |

This split follows the owner's post-approval scope. B1 has no progress/schema
or exercise-lifecycle changes and no doctor fix flag. Stop after its draft PR.
Do not create B2/B3 merely because B1 is open.

Each implementation PR updates quickstart pages, generated
[CLI help](guides/cli.md), [TUI guide](guides/tui.md), relevant task guides,
[docs/design.md](design.md) and [prompts/README.md](../prompts/README.md).
The first design-only PR intentionally leaves those files unchanged.

Run the help generator and `make docs-check`; run `make lint test` before each
draft PR, including this design PR under the standing agent rules.
Implementation evidence includes table-driven/golden results, read-only byte/
mtime checks, first-frame timings and any memory impact. Do not invent a
performance measurement for this documentation-only proposal.
Use dry-run and temporary fixtures only. Do not run up, lab/drill actions,
cloud work, sudo or host changes for validation. The agent never merges a PR.
