# 06 — Hardening found while using the lab

Needs: phases 0–3. These came from real use. In a rebuild, send this right after 05.

```text
Hardening PR. Each item has a test.

1. After a reboot the lab-* containers exist but are stopped.
   - lab up checks the Kubernetes pin, then starts the main cluster's
     nodes and lab-zot (only those), waits for nodes Ready, then plans.
     A stopped scratch cluster is reported ("lab labs start <id> starts
     it"); any other stopped lab-* container is reported with its
     `podman start` command, not started.
   - lab down starts the main cluster and lab-zot first, after the
     confirm prompt, so destroys can reach the API server.
   - lab doctor and the TUI show "○ stopped" with the fix; stopped is
     not a failure.
2. Kubernetes version gate.
   - internal/compat: one table for both modes. Each row: component,
     pinned version, the Kubernetes range its upstream STATES (Source
     cited), the modes and the stack that installs it. Optional
     StatedMaxK8s when a range comes only from upstream CI tests:
     allowed, but lab doctor warns.
   - Rows limit the version only while their stack is active (profile,
     extraStacks, --stacks, or deployed, plus dependencies).
   - lab up refuses a pin outside the table, naming the component and
     the versions that work.
   - The running version is read from /kind/version in the control
     plane node (Podman exec API). A mismatch, a stopped node or an
     unreadable version refuses with "lab down, then lab up"; never pass
     silently.
   - lab status, the TUI Home screen and lab doctor show the live version
     next to the pin.
3. client-go's own log lines (reflector "Failed to watch ... forbidden"
   while RBAC isn't ready) go to the op log, never the terminal.
4. make install / uninstall / lab version (if not done in phase 0).
5. CI speed (if not done in phase 0): per-job caches, PRs build linux
   only, the reaper only when its code changes, superseded runs
   cancelled. Measure cold and warm runs in the PR.
```
