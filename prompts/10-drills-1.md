# 10 — Drill framework, bad-deploy, oom, dns

Needs: 09 merged (sample-db exists).

```text
Drill framework and the first three drills.
Worktree: git worktree add ../devops-lab-wt-drills -b feat/drills-1 origin/main

1. Format (drills/README.md): drills/<id>/drill.yaml (id, title, level,
   stacks, timeLimit default 30m, the alert that should catch it, hints,
   cause, causes, spoilers, touches, snapshot) + README (intro = what the
   learner is told) + inject.sh, verify.sh (read-only, exit 0 =
   recovered), restore.sh, solution.sh; shared drills/lib.sh.
2. CLI: lab drills list | start <id> | start --random (hides which
   drill) | status | hint | ack <cause> (ack --list) | verify | abort |
   history [--reset [id]]. TUI screen 4 with the timer in the header.
   One drill at a time, never while a lab is active; start/verify/abort
   take the lab lock.
3. Timers: time to alert (from Alertmanager, or Prometheus ALERTS if it
   resolved; an alert from an earlier run never counts), time to detect
   (only the right cause stops it; wrong acks recorded), time to
   recover. verify describes the symptom, never the cause, until
   recovery; the cause shows at the end, on abort and with the last
   hint. e2e checks the spoilers.
4. Safety: before inject, save every snapshot object to state.json;
   restore puts them back and checks them. A failed inject restores at
   once. The time limit is a transient systemd user timer that runs
   `lab drills abort --expired` (retries if the lock is held). lab up
   restores an active drill before planning; lab doctor reports it.
5. GitOps: a fault that changes an app is deployed through Argo CD
   (Helm parameters on the Application, like Image Updater write-back),
   never Git. lab-bootstrap ignores those parameters and
   syncPolicy.automated for that app only (RespectIgnoreDifferences),
   so the realistic fix (Argo CD rollback, pausing auto-sync) works and
   kubectl edit gets self-healed away.
6. Drills, each naming its alert (add to the obs stack's lab-drills
   rules if kube-prometheus-stack lacks it; KubePodCrashLooping for: 5m):
   - bad-deploy: a release whose image exits on start. Fix: roll back
     through Argo CD.
   - oom: a release that needs more than its limit. Fix: right-size
     requests/limits in the Application; explain the trade-off.
   - dns: CoreDNS scaled to zero. Fix: scale back; explain who did it
     (managedFields). lab itself talks to the API server, not DNS.
7. Test runs (e2e, LAB_DRILLS_TEST=1) never go into the learner's
   history. e2e keeps its state in ~/.lab/e2e/state.json and holds the
   lab lock; lab doctor reports a run it left behind and lab up restores
   it (TestE2EKilledDrill).

e2e per drill on the main cluster: inject, verify fails, alert fires,
solution, verify passes; then inject + abort and everything back
exactly. Blind learner runs, commands typed one at a time. PR: time to
alert, detect, recover per drill.
```
