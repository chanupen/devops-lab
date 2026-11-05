# Drills

Break/fix drills for incident practice. Each drill injects one fault into the sample app on the main lab, starts a timer, and records how long you take to find it and to fix it. `lab drills list` shows them, `lab drills start <id>` starts one, and `lab drills start --random` starts one without saying which.

| Drill | Fault | What you should find | The fix | Alert |
|---|---|---|---|---|
| `bad-deploy` | A release whose image exits at start, deployed through Argo CD | `CrashLoopBackOff`, the new image in `argocd app history` | Roll back through Argo CD | `KubePodCrashLooping` |
| `oom` | A release that keeps 128 MB in memory, under a 64Mi limit | `OOMKilled`, exit code 137 | Right-size requests and limits in the Application | `KubeContainerOOMKilled` (added by the drills) |
| `dns` | CoreDNS scaled to zero, as a downscaler would | Lookups by Service name fail across the app | Scale CoreDNS back; explain who scaled it and why | `CoreDNSDown` (added by the drills) |
| `cert-expiry` | Two releases of sample-db through Argo CD: one-hour certificates, then a CA Issuer pointed at a CA Secret nobody created | Renewal overdue, a pending CertificateRequest, the Issuer's error, the release in Argo CD's history | Undo the release through Argo CD; the pending renewal goes through | `CertificateExpiringSoon` (added by the drills) |
| `node-loss` | The kind worker that runs podinfo-frontend stops (`podman stop`) | A NotReady node; pods not Ready, evicted after 300 s | Start the node; explain why one node took the app down (replicas, spread; a PDB does not cover a crash) | `KubeNodeNotReady` (`for` 2m in the lab) |
| `slow-db` | 300 ms of `tc netem` delay on Postgres's interface | The latency SLO burns while errors stay at zero; the query is fast inside Postgres | Remove the qdisc (a NET_ADMIN debug container) | `SampleDBLatencyFastBurn` (added by the drills) |
| `secret-drift` | The app's secret deleted in Secrets Manager (Moto), with a recovery window | ExternalSecret `SecretSyncedError`; the app still runs on the last Secret | `restore-secret`, never a new password | `ExternalSecretSyncFailed` (added by the drills) |

## Running a drill

```bash
lab drills start bad-deploy   # or: lab drills start --random
lab drills status             # the timers, the alert, what you were told
lab drills ack --list         # the causes ack takes for this drill
lab drills ack <cause>        # the cause you found: the right one stops the detect timer
lab drills hint               # the next hint (counted)
lab drills verify             # checks the fix; a pass stops the recover timer
lab drills abort              # give up: puts everything back
lab drills history            # every run, its acks, and the best times per drill
lab drills history --reset    # delete the history (--reset <id>: one drill's), after a prompt
```

The TUI shows the same on screen 4, with the running timer in the header: `enter` starts the selected drill, `R` a random one, `a` lists the causes and acks the one you pick, `v` verifies, `h` shows a hint and `x` aborts.

Each run records when it started, when its alert first fired (from Alertmanager, if the drill names one), every ack with the cause it named, when verify passed, and the hints and verify runs it took. From those come three times: **time to alert**, **time to detect** (the right ack) and **time to recover** (verify), all from the moment the fault was in. History lives in `~/.lab/state.json`.

**Ack names a cause.** `lab drills ack <cause>` takes one of the drill's `causes`. Only the real one stops the detect timer; a wrong one is recorded (history marks it ✕) and the timer runs on. A random drill offers every drill's causes, so the list does not say which drill runs.

**Verify does not give the answer away.** Until you recover, verify says what users see ("the frontend still can't reach the backend by name"), never why. The cause shows when the drill ends, when you abort, and with the last hint.

**Your history is yours.** The e2e test and maintainers' checks run with `LAB_DRILLS_TEST=1` (the e2e test sets it itself): those runs restore like any other but never reach the history.

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
| `hints` | shown one at a time by `lab drills hint`; the last one also names the cause |
| `cause`, `causes` | `causes` maps three or more suspects (words-with-dashes) to a sentence; `cause` is the key of the real one |
| `spoilers` | words verify must not print before recovery; the e2e test checks its first message |
| `touches` | namespaces whose objects the fault may change; the e2e test compares them before and after |
| `snapshot` | objects to save before the fault and put back after it: `{apiVersion, resource, namespace, name}`; one that does not exist yet is deleted on restore |

The README's intro, before its first `## ` heading, is the page: what an on-call engineer would be told. Keep the cause out of it. `--random` shows a generic page instead, and hides the drill's ID and alert until the run ends.

## Scripts

- They run with `KUBECONFIG` set to the main lab, `DRILL_ID`, `DRILL_DIR`, `DRILL_RUN`, `DRILL_STATE`, `LAB_REGISTRY_PUSH`, `LAB_REGISTRY_PULL` and the Moto variables (`AWS_ENDPOINT_URL_SECRETS_MANAGER`, `AWS_REGION`, dummy keys). `DRILL_STATE` is the run's directory (`~/.lab/drills/<run>/`) for what restore needs that is not a Kubernetes object (a secret's versions in Moto, the node the run stopped); lab removes it when the run ends. `inject.sh`, `restore.sh` have 10 minutes; `verify.sh` 60 seconds.
- `verify.sh` is read-only and idempotent. Exit 0 means recovered; otherwise its last line says why not, in one sentence. It checks the symptom first and says only what users see until it is gone; then it checks the realistic fix, not only the symptom.
- `restore.sh` must be idempotent and must not depend on what the learner did.
- Touch only the sample app's namespaces (`podinfo`, `sample-db`) and the Applications in the snapshot. A drill that must reach further says so and must keep lab usable: `dns` touches CoreDNS, `node-loss` stops a kind node (restore starts every stopped node), `slow-db` adds a qdisc in Postgres's network namespace from the kind node (restore deletes every netem rule on Postgres), `secret-drift` changes a secret in Moto (restore puts its versions back exactly).
- `lib.sh` has helpers for these: `sm` (a Secrets Manager call to Moto with curl), `pod_netns` (run `tc` or `ip` in a container's network namespace from its kind node), `kind_node_running`, `node_ready`, `kind_workers`.
- Images a drill builds go to the lab registry (`LAB_REGISTRY_PUSH`); they stay there after the drill, where they harm nothing.
- Shell checks: `shellcheck -x drills/*/*.sh drills/lib.sh`.

## Alerts

The obs stack carries the alerts the drills name. kube-prometheus-stack has `KubePodCrashLooping`; the lab sets its `for` to 5 minutes (15 upstream), so it fires within a drill. It has no alert for OOM kills or for CoreDNS being gone, so the obs stack adds `KubeContainerOOMKilled` and `CoreDNSDown` in its `lab-drills` rules (`gitops/stacks/obs/kube-prometheus-stack/values.yaml`), with `CertificateExpiringSoon` and `CertificateNotReady` (cert-manager's metrics, scraped by obs) and `ExternalSecretSyncFailed` (ESO's). `KubeNodeNotReady` waits 2 minutes in the lab (15 upstream). slow-db's alert comes from sample-db's latency SLO, `lab-slo-sample-db`: 99% of `/api/check` within 250 ms, with lab 3's fast and slow burn alerts. The obs stack has no Tempo yet, so no drill relies on traces. An alert that has not fired before you recover is recorded as "did not fire": a sign the alert is slower than you, or missing.

## Testing

`go test ./internal/drills/` covers the timers, the state, acks, test runs and restore after a simulated crash. The end-to-end test runs every drill on the main lab twice, as test runs in `~/.lab/e2e/state.json` (with `~/.lab/kubeconfig` linked beside it), holding the lab lock: inject, verify fails without a spoiler, wait for the alert, `solution.sh`, verify passes, then inject and abort. After each, every Application and the drill's namespaces must be back exactly as before (content, labels and annotations), no kind node may be stopped or NotReady, and no netem rule may be left on Postgres. If anything fails it aborts first.

A full run takes about 27 minutes on the lab host (26.2 measured with seven drills). Most of it is waiting for alerts: the test runs `solution.sh` only after the drill's alert fires, and `bad-deploy` (KubePodCrashLooping, `for: 5m`) and `cert-expiry` (renewal due two minutes after issue, two more minutes past it, then `for: 1m`) take about 5 minutes each. A test run interrupted mid-drill (Ctrl-C, a `go test` timeout, a kill) leaves its fault in the lab and its run in `~/.lab/e2e/state.json`, the same as a drill after a crash: `lab doctor` reports it (`e2e test drill <id>`), and `lab up` puts it back before it changes anything (or `lab drills abort --state ~/.lab/e2e/state.json`). The next e2e run aborts it first. `TestE2EKilledDrill` checks this: it kills a `dns` run mid-inject, then expects `lab doctor` to fail on it and `lab up` to restore the lab exactly as before (under a minute, 18 s measured; `-run E2EKilledDrill -timeout 20m`).

```bash
LAB_E2E_MAIN_KUBECONFIG=~/.lab/kubeconfig go test -tags e2e -run E2EDrills -v -timeout 60m ./internal/drills/
```

The obs rules come from Git, so test a branch that changes them with `lab up --revision <branch>` first.
