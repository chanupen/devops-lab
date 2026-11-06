# 03 — Phase 2: TUI v1

Needs: phase 1 merged. Gate: first frame in under 100 ms, measured.

```text
TUI v1 in internal/tui (Bubble Tea + Bubbles + Lip Gloss), opened by
running `lab` with no arguments.

Rules (also in CLAUDE.md):
- First frame from cached state (state.json) in under 100 ms. Never
  block the key loop: all work runs as background tea.Cmds that stream
  messages. Watch Kubernetes with client-go informers; never poll.
- Log panes: a ring buffer of 5,000 lines, at most 20 redraws a second.
- State shown with symbol and colour together: ● healthy, ◐ progressing,
  ○ off, ✕ failed.
- Every TUI action logs the equivalent CLI command.

Screens (keys 1–6): 1 Home (mode, profile, nodes, apps, URLs, stopped
containers), 2 Stacks (deployed state, live app health), 3 Labs,
4 Drills, 5 Ops log (every operation with its streamed output and CLI
command), 6 Cost (cloud). Screens 3, 4 and 6 can be placeholders now.

Keys: `:` palette with fuzzy search (`:up obs`, `:mode cloud`), `/`
filter, `enter`/`esc`, `u`/`d` up/down the selected stack (`d` asks
first), `U` the whole profile, `r` retry a failed stack, `l` logs,
`K` hand off to k9s on the lab context and come back, `m` mode, `?`
help.

Lock: a TUI that can act holds the lab lock while open. If it can't get
it, it opens read-only ("read-only: another lab is running (pid …)" in
the header), refuses actions, and takes over when the holder is done.
Actions run one at a time.

Tests: golden files for every screen (fixed width), a test that the
first frame renders from state alone with no cluster, and a benchmark
for the first frame. Put the measured first-frame time in the PR.
```
