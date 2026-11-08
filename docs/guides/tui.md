# Use the terminal UI

[Docs index](../README.md) · [CLI reference](cli.md) · [Assistant](assistant.md)

Start from a configured checkout with an installed `lab`.

```sh
lab
```

**What you will see:** cached Home state first, then live updates. The header
shows mode and profile; the bottom strip shows actions and their CLI command.
Views do not wait for operations. Output retains 5,000 lines, with at most
20 redraws per second.

> **Danger and cost:** action keys really run operations. Local up changes
> containers; down deletes stack data. A cloud TUI changes billed AWS resources.
> Get the same approvals as for the CLI; use the
> [cloud session guide](../getting-started/cloud.md). Navigation does not tear down
> a lab. Quitting during up can leave partial resources; it is not cleanup.

## Screens

| Key | Screen and what you will see | CLI equivalent |
| --- | --- | --- |
| `1` | Home: profile, containers, nodes, stack health, routes and active exercise | `lab status`; exercise status commands below |
| `2` | Stacks: catalog, saved/live state, dependencies and Argo CD apps | `lab plan`, `lab status`; selection is UI-only |
| `3` | Labs: six entries, progress and the selected step's task/expected result | `lab labs list`, `lab labs status`; read the exercise README for step text |
| `4` | Drills: seven entries, alerts, active timers, hints and latest recovery result | `lab drills list`, `lab drills status`, `lab drills history` |
| `5` | Ops: operations newest first, duration, result and saved log | No dedicated log-list command; state stores history and CLI operations print log paths |

`6` only reports that the screen comes later. There is no implemented Cost
screen. `m`, a general `v` verify key, and `l` logs are not implemented global
shortcuts. Verify is screen-specific; open output on Ops with Enter.

## Global keys

| Key | What you will see | CLI equivalent |
| --- | --- | --- |
| `1`–`5` | Selected screen | Reports in the screen table |
| `?` | Key help for the current screen | `lab --help` is CLI help, not identical key help |
| `U` | Profile up starts; progress in the strip/Ops | `lab up` |
| `:` | Fuzzy command palette with the exact command beside each entry | Commands in the palette table |
| `K` | k9s on the lab kubeconfig; quitting k9s returns here | `k9s --context <lab-context> --kubeconfig <lab-kubeconfig>` |
| `a` | Firing-alert picker; on Drills, investigation of the running drill | `lab assist alert <name> --rules-first` or `lab assist drill <id> --rules-first` |
| `q`, Ctrl-C | TUI exits and releases its lock | UI-only; **not** `lab down` |

Outside overlays, `u`, `d` and `r` require a selected stack on screen 2.
In key help, any key closes help; `q` quits. A confirmation accepts only
`y` or `Y`; any other key cancels. Ctrl-C always quits.

**K9s safety:** the handoff uses the operator's kubeconfig. A read-only TUI
does not restrict that identity's RBAC. Do not use k9s to bypass the lab lock
or GitOps ownership.

## Stacks keys

| Key | What you will see | CLI equivalent |
| --- | --- | --- |
| `j`/Down, `k`/Up | Cursor moves through filtered stacks | UI-only |
| `g`/Home, `G`/End | First or last visible stack | UI-only |
| `/`, typing | Case-insensitive name filter | UI-only; `lab plan` lists the whole plan |
| Enter while filtering | Filter retained; typing stops | UI-only |
| Esc | Filter cleared | UI-only |
| `u` | Selected stack and dependencies applied | `lab up --stacks <stack>` |
| `d` | Teardown plan and confirmation, including dependents | `lab down --stacks <stack>` |
| `r` | Failed selected stack retried; otherwise a refusal | `lab up --stacks <stack>` |

**Destructive:** confirm down only after reviewing its dependents. The engine
can remove more than the one named stack.

## Labs keys

| Key | What you will see | CLI equivalent |
| --- | --- | --- |
| `j`/Down, `k`/Up | Move between labs, or scroll an open step | UI-only |
| Enter | Current step text opens; **does not start the lab** | Read `labs/<id>/README.md` |
| `n`, `p` | Next/previous step; opens the current step if closed | UI-only |
| `v` | Step verification output and saved progress | `lab labs verify N` |
| `h` | Next hint for the displayed/current step | `lab labs hint N` |
| Esc | Return from step text to lab details | UI-only |

Start a lab with the CLI first. Verification/hints refuse if the selected lab
is not active. A passing verify updates progress; opening a step is not proof
you finished it. Hint/verify can persist progress even though verification
scripts do not change workloads. There are no TUI start/reset/stop keys for labs.
Use the [existing exercise instructions](../../labs/README.md).

## Drills keys

> **Changes the lab:** starting a drill deliberately injects a fault. These
> controls are a reference, not a first-hour tour. Drills are local only.
> No drill is run to generate or validate this guide.

| Key | What you will see | CLI equivalent |
| --- | --- | --- |
| `j`/Down, `k`/Up | Selected drill changes | UI-only |
| Enter | Start confirmation, then active timers | `lab drills start <id>` |
| `R` | Random-start confirmation; the selected fault stays hidden | `lab drills start --random` |
| `A` | Cause picker; correct ack stops detection time, wrong ack is recorded | `lab drills ack --list`, then `lab drills ack <cause>` |
| `v` | Recovery check; a pass ends the drill and restores its saved state | `lab drills verify` |
| `h` | Next counted hint | `lab drills hint` |
| `x` | Abort confirmation, then restore | `lab drills abort` |
| `a` | Read-only assistant note, not an ack or a fix | `lab assist drill <id> --rules-first` |

In the cause picker, `j`/Down and `k`/Up move, `1`–`9` select a row, Enter
acks it, and Esc/`q` cancels. Only one drill can run, never beside a started
lab. Read [drill safety](../../drills/README.md) before using these keys.

## Ops and output viewer

| Context/key | What you will see | CLI equivalent |
| --- | --- | --- |
| Ops: `j`/Down, `k`/Up | Selected operation moves | UI-only |
| Ops: `g`/Home, `G`/End | Newest or oldest operation | UI-only |
| Ops: Enter | Live output or saved log, with its CLI command at the top | Read the printed log path; no dedicated `lab logs` command |
| Viewer: `j`/Down, `k`/Up | One-line scrolling | UI-only |
| Viewer: PageDown/Space/`f`, PageUp/`b` | Page scrolling | UI-only |
| Viewer: `g`/Home, `G`/End | Start or end; end follows arriving output | UI-only |
| Viewer: Esc/`q` | Viewer closes, operation continues | UI-only |

A truncated pane says how many older lines were dropped. The log file still
contains them. A completed operation can have no recorded log; the viewer
says so instead of inventing output.

## Palette

| Entry | What you will see | CLI equivalent |
| --- | --- | --- |
| `:up` | Whole profile applies | `lab up` |
| `:up <stack>` | Stack and dependencies apply | `lab up --stacks <stack>` |
| `:down <stack>` | Plan and destructive confirmation | `lab down --stacks <stack>` |
| `:status` | Read-only status output viewer | `lab status` |
| `:doctor` | Read-only readiness report | `lab doctor` |
| `:plan` | Read-only stack plan | `lab plan` |

Type to fuzzy-match. Down/Ctrl-N/Tab and Up/Ctrl-P/Shift-Tab move through
matches. Enter runs the selected entry; Esc closes the palette.
It is not a shell and does not offer a bare whole-lab `:down`.

## Assistant overlay

Outside Drills, `a` loads firing alerts. Use `j`/Down or `k`/Up to select,
then `a` or Enter to investigate. The equivalent command includes exact
`--label key=value` selectors, config/state and any tracing certificate.
The [assistant guide](assistant.md) explains notes and their limits.

While running, Esc cancels the request and leaves a cancelled pane. Esc again
closes it. `q` closes/cancels the pane, not the TUI; Ctrl-C quits the TUI.
Afterward, the output-viewer scrolling keys apply. **Esc does not abort a drill.**

## Locks and state

A TUI holding the lab lock permits one mutating operation at a time.
Another TUI opens read-only, reports its holder, and can take over after the
holder exits. Read-only reports and assistant reads still work.
Quit the mutating TUI before using a separate CLI for up/down.

Symbols appear with colour: `●` healthy, `◐` progressing, `○` off and `✕` failed.
Saved `applied` state is not a live health guarantee. Use status and the
doctor report if the panes disagree. The TUI uses the selected config's mode;
root `lab` has no `--mode` flag or live mode-switch key. CLI commands whose
help advertises `--mode` can override the mode for that command.
