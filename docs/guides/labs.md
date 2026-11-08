# Work through the hands-on labs

[Docs index](../README.md) · [Drills](drills.md) · [Profiles/stacks](profiles-and-stacks.md)

Labs teach platform work in ordered steps. Verification records your progress;
it does not execute the solution. Read each task, make the change in its scoped
environment, then verify. The README's step text is also used by CLI/TUI.

## Order, time and prerequisites

Use this learning order; the manager does not require earlier labs to be complete.
All require the configured platform and their listed tools on PATH.

| Order/lab | Time / level | Modes | Environment and stacks | Tools |
| --- | --- | --- | --- | --- |
| [1: Argo CD bootstrap](../../labs/01-argocd-bootstrap/README.md) | 45m / beginner | Local | Isolated scratch cluster | kubectl, Helm, argocd, jq |
| [2: Image promotion](../../labs/02-image-promotion/README.md) | 40m / intermediate | Local, cloud | Main cluster: registry, cluster | Podman, kubectl, jq, curl |
| [3: SLO and burn rates](../../labs/03-slo-burn-rate/README.md) | 45m / intermediate | Local, cloud | Main cluster: obs, sample-app | kubectl, jq, curl, Podman |
| [4: Secret rotation](../../labs/04-secret-rotation/README.md) | 40m / intermediate | Local, cloud | Main cluster: secrets, sample-db | Podman, kubectl, jq |
| [5: Digest policy](../../labs/05-kyverno-digest/README.md) | 35m / intermediate | Local, cloud | Main cluster: policy | kubectl, jq, Podman |
| [6: Karpenter](../../labs/06-karpenter/README.md) | 50m / advanced | Cloud only | Main cluster: cluster | Podman, kubectl, jq |

Trivy, Skopeo, Sloth, Kyverno CLI and AWS CLI wrappers use digest-pinned
rootless Podman images. Individual pages supply the needed setup.
Start checks declared tools and prints pinned install commands if missing.

## Choose and prepare

**Read-only catalog.**

```sh
lab labs list --mode local
```

**What you will see:** six entries, mode restrictions, estimated time, level
and progress. Lab 6 says it is not in local mode.

```sh
lab labs --mode local
```

**What you will see:** the same list, with a mode-catalog completion summary,
current step/session, lifetime completion count and best active attempt.
Missing best time prints `-`, not zero. `--help` still shows command help.
Listing takes no lab lock and saves nothing.

The durable ledger is beside current progress in the selected state file.
The first successful final-step transition counts once; repeated verification
does not count again. Reset starts a new timed attempt. Pause/resume uses the
existing active-time accounting; displayed session time keeps its old meaning.
Scratch stop removes current steps, not historical counts/bests.
Imported version-1 completions have unknown best time until a fresh known attempt.

> **Changes and isolation:** one lab can be active, never alongside a drill.
> Scratch lab 1 gets `lab-scratch-01-argocd-bootstrap`, no host ports, and its
> own kubeconfig. Main-cluster labs work only in their `labNN-*` namespaces
> and cluster objects labelled `lab.devops/lab=<id>`.
> Never aim these tasks at an unrelated cluster.

For main-cluster labs, prepare their declared stacks using the local
[getting-started](../getting-started/local.md) and profile guides.
Do not assume the default contains secrets, sample-db or policy.
Quit the acting TUI before CLI start/reset/stop; these commands take the lab lock.

## Start and select the right environment

**Changes the lab; lab 1 creates a scratch cluster.**

```sh
lab labs start 1 --mode local
```

**What you will see:** setup progress, the introductory page and current step.
Use another table ID for a different lab. The number is enough.
Starting the same active lab resumes it; a different active lab is refused.
Scratch start refuses an existing cluster it does not own.

```sh
lab labs env --mode local
```

**What you will see:** shell export statements for the active lab's kubeconfig,
registry and local Moto environment. Verify uses that environment automatically;
your interactive commands do not unless you select it too.

**Private shell configuration:** save/review the exports outside Git, then load
only the output from your own `lab` binary:

```sh
umask 077
lab_env="$(mktemp "$HOME/.lab/lab-env.XXXXXX")"
lab labs env --mode local > "$lab_env"
```

**What you will see:** no output; a new owner-only export file is written.
Inspect it locally before sourcing; never attach it to a report.

```sh
. "$lab_env"
```

**What you will see:** normally no output. This shell now targets the active
lab. Check `KUBECONFIG` before any mutating kubectl command.
Scratch lab 1 must not use `~/.lab/kubeconfig` or `~/.kube/config`.

## Read, hint and verify each step

The individual page has a Steps index and detailed `Step N` tasks below its
lifecycle sections. Required resource names, pins and outcomes are in the task.
Complete it yourself; full reference commands stay in solution scripts.

```sh
lab labs status --mode local
```

**What you will see:** the active lab, current step and time/progress.
Main-cluster time counts the active session; scratch time accounts for its
container uptime and does not keep counting while the scratch cluster is stopped.

```sh
lab labs hint 1 --mode local
```

**What you will see:** the next hint for step 1. Omit the number to use the
current step. Hints help you choose evidence/tools; they are not complete solutions.

**Read-only workload verification; progress is saved.**

```sh
lab labs verify 1 --mode local
```

**What you will see:** a step result. Passing advances progress; failure says
what is missing. Each script is idempotent and has a 60-second timeout.
Use the next step number, or omit it for the current step.

The TUI's Labs screen opens step text, verifies and reveals hints. It does
not start/reset/stop labs. See [its keys](tui.md#labs-keys).

## Reference solutions

**Spoiler — prints a script; does not run it.**

```sh
lab labs solution 1 --mode local
```

**What you will see:** a confirmation prompt, then the reference answer for the
selected step. Noninteractive use requires `--yes`; it still only displays files.
Scripts live in `labs/<id>/solution/NN-solve.sh`; the learner READMEs label
their links. Do not execute one without reviewing its changes and environment.
After using an answer, still verify the step.

## Reset and stop

> **Destructive:** reset removes the active lab's exercise resources and
> resets progress. Review each page's exact cleanup scope. Main-cluster stop
> alone does **not** remove its workloads. Scratch stop deletes its cluster
> and resets its steps/time.

For a main-cluster lab, clean resources before ending the session:

```sh
lab labs reset --mode local
```

**What you will see:** a confirmation prompt, scoped cleanup output, and reset
steps/hints. Time spent and verification attempts are retained.
Host work directories and named tool-cache volumes can remain;
their removal needs separate approval.

```sh
lab labs stop --mode local
```

**What you will see:** a confirmation and a summary of where you stopped.
For scratch, its cluster is deleted and a fresh start begins at step 1.
For main-cluster, progress survives stop and its session time resets.
Reset first if you wanted its workloads removed.

```sh
lab labs status --mode local
```

**What you will see:** no active lab after a successful stop.
Stop any manual port-forward with Ctrl-C and close the exercise shell to avoid
keeping its kubeconfig/credentials selected.

## Cloud sessions

> **Billed — approval required:** labs 2–6 use the already-approved AWS lab.
> All cloud task mutations and cleanup need session approval. Work stays in
> `ap-south-1` with the required lab/owner/expiry tags.
> Lab 6 launches and terminates real nodes; its queue message is not read-only.
> TTL and budgets are not hard spending caps.

Use [cloud getting started](../getting-started/cloud.md) first. Pass
`--mode cloud` consistently to start, env, hint, verify, reset, stop and status.
Cloud env output includes temporary AWS credentials; do not print, log or commit
them. Review/save privately as above, using the cloud command instead.
Refresh the session/environment if credentials expire.

ECR replaces local Zot: use HTTPS verification, authenticate to the approved
registry, and build node images for `linux/arm64`. Never disable ECR TLS.
Lab-created AWS resources must carry `LAB_AWS_TAGS`, including `expires-at`.
An emulator endpoint must not override real Secrets Manager in the cloud shell.

Resetting/stopping an exercise does not destroy EKS or stop all billing.
Finish the full approved down/leftovers sequence. On failures, keep state and
collect brief evidence; do not debug indefinitely while resources are billed.
