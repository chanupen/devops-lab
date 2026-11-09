# Troubleshoot the lab

[Docs index](../README.md) · [Local guide](../getting-started/local.md) ·
[Cloud guide](../getting-started/cloud.md)

Start with a read-only report, not a reset.

```sh
lab status --mode local
```

**What you will see:** saved stack state plus live health when a cluster exists.
`applied` alone does not mean healthy. Operation failures print stack log paths.

```sh
lab doctor --mode local --profile minimal --read-only
```

**What you will see:** named checks, details, suggested fixes and a summary.
`✕` failures exit non-zero; `◐` warnings need review. Skipped/stopped entries
are explained in the [doctor reading guide](../getting-started/local.md#read-lab-doctor).
Choose `full` only if you want that threshold and AI checks.

The read-only registry probe uses GET and checks loopback bindings and the
selected lab's mirror file. It does not test upload permission. Ordinary
doctor retains its temporary upload-and-cancel probe.

```sh
lab next
```

**What you will see:** one exact suggested command, its reason and effect.
Failed doctor checks precede ordinary startup or exercise suggestions.
An active/interrupted drill gets explicit status/recovery guidance first;
its identity stays hidden if it is random. No suggested command is executed.
Warnings marked `fix: allowed` or `fix: accepted` remain visible in doctor
but do not divert next from an eligible exercise. This includes Kyverno's
accepted Kubernetes 1.36 tested-range warning. Actionable warnings and failures
still suggest doctor.

Start rejects an active lab/drill, a busy lock, insufficient available RAM
or changed consent. Finish the exercise explicitly, quit the acting TUI,
close memory-heavy apps, or rerun the preview as directed. Never remove a
held lock or erase state to bypass a refusal.

> **Danger and cost:** do not delete state, kubeconfigs or Podman volumes as
> a repair. Get approval before sudo, host configuration changes, destructive
> cleanup or cloud apply/destroy. Keep listeners on loopback. A failed cloud
> up is still billed; collect brief evidence and run the approved cleanup
> sequence immediately, not an open-ended debugging session.

## State and progress migration

State version 2 adds durable lab/drill summaries, separate from current steps
and the bounded drill history. Reading version 1 migrates in memory only;
lists, next and cached TUI views never save an upgrade. The next successful
existing state mutation writes version 2 atomically with private permissions.
Local and cloud files stay separate; migration never contacts AWS.

Migration seeds one lab completion per retained legacy `Completed` timestamp,
with unknown best time. Legacy in-progress timing stays unknown until a fresh
attempt/reset; missing uptime or an unrecorded restart cannot earn a best.
Retained non-test drill runs seed counts and bests. Earlier trimmed/reset runs
or deleted scratch progress cannot be reconstructed. Future totals are durable.

Corrupt, unversioned and future-version files are refused without replacement.
Keep the original file and inspect the error with a compatible binary; never
erase state to bypass a failure. Older binaries reject version 2. Do not edit
the version number back or discard progress/recovery fields to downgrade.

## Host and tool checks

| Symptom/check | Likely cause | Fix and what you will see |
| --- | --- | --- |
| Podman not rootless or not cgroup v2 | Wrong user/session or host configuration | Use the normal user and approved cgroup-v2 setup; doctor should report rootless/v2 |
| `podman.socket` is not active | User API socket disabled | Enable the user socket as in the local guide; doctor reports `active` |
| `cgroup delegation` reports missing controllers | User service lacks delegation | Review the proposed `Delegate=cpu cpuset io memory pids` host change, then re-login; doctor lists all five |
| inotify below 524288/512 | Host watch limits too low | Approve persistent sysctl changes; doctor reports watches/instances above the thresholds |
| iptables modules missing | Host kernel/network setup incomplete | Review module loading for IPv4 and IPv6; the module check passes |
| SELinux/device access fails | `container_use_devices` disabled | Approve the SELinux change, not disabling enforcement; the device check passes |
| Go tool is gccgo or too old | PATH selects a different toolchain | Select the module-compatible Go toolchain; doctor identifies Go, not gccgo |
| Missing kind/kubectl/Helm/k9s/IaC/AWS tool | Missing executable or PATH entry | Install the reported tool after review; doctor shows its version. IaC/AWS are checked even locally |
| `lab` reports an old pin after pulling source | Installed binary is stale | Run `make install` from the checkout; it prints the installed path/version and warns about PATH precedence |
| `memory` failure or warning | Insufficient total/free memory | Close unrelated workloads or select a smaller configured profile; doctor reports headroom. Extra stacks need more than the profile-only threshold |
| `lan listeners` fails | A lab port is bound off loopback | Fix the owning listener; Ollama Quadlet must publish `127.0.0.1:11434:11434`. The check passes only when lab ports are loopback |
| Main/scratch/cache containers are `stopped` | Host reboot or stopped containers | Approved local up restarts main nodes/Zot; use the active lab's start workflow for its scratch cluster. Do not start unrelated containers |

## Downloads and GitOps

| Symptom | Likely cause | Fix and what you will see |
| --- | --- | --- |
| ghcr.io pulls or chart downloads crawl/time out | Slow GitHub CDN address from this network | Doctor measures the path. Review `zot.addHosts` to pin a tested CDN address inside Zot only; prefetch retries should complete. Never edit host `/etc/hosts` |
| Lab 1's Argo CD chart download times out | One GitHub release CDN address is unreachable | Use the exercise's pinned OCI chart source; its download and verify should complete |
| Up refuses a different main-cluster Kubernetes version | kind cannot upgrade an existing cluster in place | Approved down/up recreates it at the pin; Zot cache remains. Data is lost, so this is destructive |
| Apparent version mismatch despite the image name | kind pulled by digest, not a version tag | Trust the version read from the node by status/doctor, not parsing the image name |
| `lab-bootstrap` Unknown / publickey permission denied | Wrong deploy key, stale repo Secret or transient GitHub issue | Check read-only key access, then use the controlled refresh below; app revision/health should recover |
| Revision cannot resolve after a branch merge | GitOps tracks a deleted branch | Approved plain up returns GitOps to `main`; status should show `main` |
| Kyverno remains OutOfSync on CRDs | Empty metadata drift or client-side size limit | Keep the overlay's CRD metadata ignores and server-side apply; repair in GitOps, not competing manual installs |
| App TLS verification fails | Self-signed Gateway certificate not trusted, or wrong lab certificate | Export only the correct public certificate using getting-started; a `curl --cacert` request succeeds. Never extract the private key or disable TLS globally |
| Another lab is already running / TUI read-only | Shared mutation lock is held | Quit the acting TUI or wait for its operation. Views remain available; do not remove a live lock file |

### Refresh repository credentials

**Changes the lab:** use only after checking that the dedicated deploy key is
registered read-only with the repository and its private file is outside Git.
Do not paste key contents into issue reports.

```sh
lab up --mode local --stacks gitops --refresh-repo
```

**What you will see:** plan/progress and repo credential refresh, then GitOps
health. If the revision itself was deleted, plain up targets `main`.

### Recheck after a fix

```sh
lab status --mode local
```

**What you will see:** current revision, stack state and live health.
Save a short redacted error and the relevant log excerpt if it still fails.
Do not dump environment variables, Secrets or kubeconfig contents.

## Cloud: recognize, then clean up

These are diagnostic references. **No cloud commands are run for this page.**
The [cloud guide](../getting-started/cloud.md) contains the approved login,
plan, up/tunnel/down/leftovers sequence and expected results.

| Symptom | Likely cause | Fix and what you will see |
| --- | --- | --- |
| `source_profile requires role_arn, profile devops` | The source profile points to itself | Operator reviews/removes the erroneous source_profile line; fresh MFA login reports the intended account/role. Agents must not edit AWS profiles |
| Baseline package missing | Reaper ZIP not built in this checkout | Rebuild the local ZIP before baseline planning; the package check succeeds |
| Reaper dry-run, stale/error heartbeat, or teardown running | Cleanup fallback is not ready or already active | Stop. Require a clean recent reaper self-test and no build in progress; cloud doctor must pass before up |
| EKS version fails support check | Pin outside supported component/EKS versions | Review the cloud compatibility choice; doctor should report standard support. Do not bypass it |
| Managed policy does not exist during plan | Wrong IAM policy ARN | Review the module against AWS's actual policy name before apply; policy preflight passes, not a skipped check |
| node-exporter Pending / too many pods | VPC CNI IP capacity too low | Preserve prefix delegation and critical DaemonSet priority in the modules; pod capacity/health recovers on the next approved recreation |
| Repo-server crash-loops on tiny cloud nodes | Requests let Karpenter pack too tightly | Preserve measured requests and the 4 GiB NodePool minimum; do not debug indefinitely on billed time |
| Image will not run on cloud nodes | No arm64 build | Build/push the correct architecture through the approved registry workflow; image pulls and startup succeed |
| Down reports instances/volumes/fleets after deletion | Tagging index lags | Direct checks classify terminated/NotFound entries as stale. Real leftovers and lookup errors remain failures |
| Up failed but state looks empty | Resources can exist outside saved applied stacks | Follow approved down and direct leftovers checks; the cleanup gate is no real resources, not an empty state file |

Budgets and TTL do not stop charges instantly. Never remove the permanent
baseline, disable the reaper, or bypass exhausted-cleanup safeguards to retry.

## AI and exercise state

| Symptom | Likely cause | Fix and what you will see |
| --- | --- | --- |
| NVIDIA/CDI/VRAM warning on minimal | AI not selected; optional host checks still run | Review the warning; do not install AI to silence it. AI/full requires these checks to pass |
| Ollama digest/model check fails | Wrong runtime image or model absent | Keep the pinned Quadlet and selected local model; doctor reports the expected digest/model |
| Pods cannot reach Ollama through host.containers.internal | Rootless network cannot reach host loopback that way | Use the Quadlet on kind with its alias/address and `ollama.ai.svc`; the in-cluster check passes |
| Ollama EndpointSlice missing / service proxy fails | Argo excludes endpoints; service proxy reads Endpoints differently | Let lab reconcile `ollama-host`; use doctor's actual cluster reachability check, not a guessed proxy test |
| Langfuse rejects legacy trace-create events | Runtime expects OTLP | Use the shared runner's OTLP tracing path; trace succeeds, not a legacy events request |
| Assistant missing/ambiguous alert | No exact firing alert or duplicate selectors | Choose exact labels/namespace; a matching note appears or it abstains/errors. Do not inject a demonstration fault |
| Assistant timed out or note incomplete | Missing evidence, model failure or bounded runtime | Review retained evidence and the error manually. No fix was executed; do not treat partial output as a diagnosis |
| Doctor reports an active or interrupted drill/e2e | Saved restore state still exists | Finish recovery or use the controlled abort path below; never delete snapshots/history to hide it |
| Lab scratch progress/time resets after stop | Scratch cluster is removed | Expected behavior: a new scratch start begins at step 1; main-cluster lab progress is retained |

### Restore an interrupted drill

> **Changes the lab:** abort restores saved workload/non-Kubernetes state.
> Use only for a known active drill, with the correct config/state. Do not
> discard the restore record. This reference is not executed during docs work.

```sh
lab drills abort
```

**What you will see:** restoration progress and a finished/aborted result, or
`no drill running`. An interrupted e2e can have separate `~/.lab/e2e/state.json`;
follow doctor's recorded recovery guidance for that run, not the learner's state.

```sh
lab drills status
```

**What you will see:** no active drill after successful restoration.
A restore error is not success; retain its state and investigate.

These symptom/cause/fix entries come from doctor messages and
[known issues](../../prompts/91-known-issues.md). That engineering log retains
the historical details; this page is the reader-facing starting point.
