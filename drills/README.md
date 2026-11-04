# Drills

Break/fix drills for incident practice. Each drill injects one fault into the sample app on the main lab, starts a timer, and records how long you take to find it and to fix it. `lab drills list` shows them, `lab drills start <id>` starts one, and `lab drills start --random` starts one without saying which.

| Drill | Fault | What you should find | The fix | Alert |
|---|---|---|---|---|
| `bad-deploy` | A release whose image exits at start, deployed through Argo CD | `CrashLoopBackOff`, the new image in `argocd app history` | Roll back through Argo CD | `KubePodCrashLooping` |
| `oom` | A release that keeps 128 MB in memory, under a 64Mi limit | `OOMKilled`, exit code 137 | Right-size requests and limits in the Application | `KubeContainerOOMKilled` (added by the drills) |
| `dns` | CoreDNS scaled to zero, as a downscaler would | Lookups by Service name fail across the app | Scale CoreDNS back; explain who scaled it and why | `CoreDNSDown` (added by the drills) |

## Running a drill

```bash
lab drills start bad-deploy   # or: lab drills start --random
lab drills status             # the timers, the alert, what you were told
lab drills ack                # you have found the fault: stops the detect timer
lab drills hint               # the next hint (counted)
lab drills verify             # checks the fix; a pass stops the recover timer
lab drills abort              # give up: puts everything back
lab drills history            # every run, and the best times per drill
```

The TUI shows the same on screen 4, with the running timer in the header: `enter` starts the selected drill, `R` a random one, `a` acks, `v` verifies, `h` shows a hint and `x` aborts.

Each run records when it started, when its alert first fired (from Alertmanager, if the drill names one), when you acked and when verify passed, and the hints and verify runs it took. From those come three times: **time to alert**, **time to detect** (ack) and **time to recover** (verify), all from the moment the fault was in. History lives in `~/.lab/state.json`.

Rules:

- One drill at a time, and not while a lab is started (`lab labs start` also refuses while a drill runs). Drills run on the main lab, in local mode.
- `start`, `verify` and `abort` take the lab lock, like `lab up`; an open TUI runs them itself.
- A passing verify ends the drill and puts back what it changed, as Git has it. Your fix may leave the app working but not as Git has it (an emergency rollback leaves auto-sync off), so lab finishes the job.
- Every drill has a time limit (30 minutes unless `timeLimit` says otherwise). At the limit lab puts everything back on its own.

## Safety: how a fault never stays behind

- **Saved before the fault.** Before `inject.sh` runs, lab reads every object in the drill's `snapshot` list and writes it into `~/.lab/state.json` (`activeDrill.snapshot`), with the drill's directory. Any lab process can restore from that file alone.
- **Restore** puts each saved object back (content, labels and annotations; an object that did not exist is deleted, one that was deleted is created), runs `restore.sh`, then compares every saved object with the cluster. If one is not back, the drill stays active with the error, and `lab drills abort` tries again. Restore can run any number of times.
- **After a crash or a reboot**, the state file still holds the drill. `lab drills abort` restores it; `lab up` restores it before it plans; `lab doctor` reports it (a warning while it runs, a failure past its limit or when its inject or restore did not finish).
- **The time limit** is a transient systemd user timer (`lab-drill-<run>`) that runs `lab drills abort --expired` just after the deadline, and retries every minute while another lab holds the lock. An open TUI holds the lock, so it restores an expired drill itself. A timer does not survive a reboot; `lab up` and `lab doctor` cover that.
- **A failed inject** is restored at once.
- **The cluster stays reachable.** lab talks to the API server, which does not need cluster DNS, and reads alerts through the API server's service proxy (no port-forward), so the dns drill cannot lock lab out.

## GitOps: faults that self-heal does not undo, and never reach Git

Argo CD self-heals every app from Git, so a fault made with `kubectl` would be gone in seconds, and a fault in Git would reach `main`. The drills that change an app (`bad-deploy`, `oom`) do neither. They release the way a CD tool with Argo CD write-back does (Argo CD Image Updater, a pipeline that runs `argocd app set -p`): they set Helm parameters on the app's Application. Argo CD then deploys the fault itself, keeps it with self-heal, and records it in the app's history. Git never changes.

`lab-bootstrap`, the app-of-apps that renders every Application from Git, would undo those parameters too. For the drill's app only, lab adds to it (and removes afterwards):

```yaml
spec:
  ignoreDifferences:
    - group: argoproj.io
      kind: Application
      name: podinfo-frontend
      namespace: argocd
      jsonPointers: [/spec/source/helm/parameters, /spec/syncPolicy/automated]
  syncPolicy:
    syncOptions: [RespectIgnoreDifferences=true]
```

That leaves the parameters a CD tool writes, and the auto-sync switch, to the people who run the app: the usual setup for an app-of-apps whose apps are released by a tool, and what an emergency rollback needs (Argo CD refuses to roll back while auto-sync is on). So the realistic fix is the one that works: roll back or change the desired state through Argo CD. `kubectl edit` on the Deployment is undone by self-heal, and the verify scripts check that the running release came from Argo CD.

Both Applications are in the drill's snapshot, so restore puts `lab-bootstrap` and the app back exactly as they were, and Argo CD syncs Git's release again. `dns` changes CoreDNS, which kind installs and Argo CD does not manage; its snapshot is the `coredns` Deployment.

## Format

```text
drills/<id>/
  drill.yaml     id, title, level, stacks, timeLimit, alert, hints, touches, snapshot
  README.md      what the learner is told (the intro), then what happened and the fix
  inject.sh      puts the fault in
  verify.sh      exit 0 = recovered; otherwise the last line says why not
  restore.sh     after lab put the saved objects back: anything else, then wait until healthy
  solution.sh    the reference fix
drills/lib.sh    shared helpers (argocd in core mode, release, waits)
```

`drill.yaml`:

| Field | Meaning |
|---|---|
| `id` | words-with-dashes, the same as the directory |
| `title`, `level` | shown in the list |
| `stacks` | main-lab stacks the drill needs; `--random` picks only drills whose stacks are up |
| `timeLimit` | when lab restores on its own (default `30m`) |
| `alert` | `name` and `labels` of the Prometheus alert that should catch the fault |
| `hints` | shown one at a time by `lab drills hint` |
| `touches` | namespaces whose objects the fault may change; the e2e test compares them before and after |
| `snapshot` | objects to save before the fault and put back after it: `{apiVersion, resource, namespace, name}`; one that does not exist yet is deleted on restore |

The README's intro, before its first `## ` heading, is the page: what an on-call engineer would be told. Keep the cause out of it. `--random` shows a generic page instead, and hides the drill's ID and alert until the run ends.

## Scripts

- They run with `KUBECONFIG` set to the main lab, `DRILL_ID`, `DRILL_DIR`, `DRILL_RUN`, `LAB_REGISTRY_PUSH` and `LAB_REGISTRY_PULL`. `inject.sh`, `restore.sh` have 10 minutes; `verify.sh` 60 seconds.
- `verify.sh` is read-only and idempotent. Exit 0 means recovered; otherwise its last line says why not, in one sentence. It checks the realistic fix, not only the symptom.
- `restore.sh` must be idempotent and must not depend on what the learner did.
- Touch only the sample app's namespaces (`podinfo`, `sample-db`) and the Applications in the snapshot. A drill that must reach further says so (`dns` touches CoreDNS) and must keep lab usable.
- Images a drill builds go to the lab registry (`LAB_REGISTRY_PUSH`); they stay there after the drill, where they harm nothing.
- Shell checks: `shellcheck -x drills/*/*.sh drills/lib.sh`.

## Alerts

The obs stack carries the alerts the drills name. kube-prometheus-stack has `KubePodCrashLooping`; the lab sets its `for` to 5 minutes (15 upstream), so it fires within a drill. It has no alert for OOM kills or for CoreDNS being gone, so the obs stack adds `KubeContainerOOMKilled` and `CoreDNSDown` in its `lab-drills` rules (`gitops/stacks/obs/kube-prometheus-stack/values.yaml`). An alert that has not fired before you recover is recorded as "did not fire": a sign the alert is slower than you, or missing.

## Testing

`go test ./internal/drills/` covers the timers, the state, and restore after a simulated crash. The end-to-end test runs every drill on the main lab twice, into a throwaway state file: inject, verify fails, wait for the alert, `solution.sh`, verify passes, then inject and abort. After each, every Application and the drill's namespaces must be back exactly as before (content, labels and annotations). If anything fails it aborts first.

```bash
LAB_E2E_MAIN_KUBECONFIG=~/.lab/kubeconfig go test -tags e2e -run E2EDrills -v -timeout 90m ./internal/drills/
```

The obs rules come from Git, so test a branch that changes them with `lab up --revision <branch>` first.
