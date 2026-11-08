# Use the local assistant

[Docs index](../README.md) · [TUI controls](tui.md#assistant-overlay) ·
[Troubleshooting](troubleshooting.md)

The assistant drafts an evidence-backed incident note. It does not fix the
lab. CLI, TUI and the Alertmanager receiver share the same runner.
The default assistant model is host Ollama's `llama3.2:3b`.
Cloud AI is an intentional stub.

> **Safety and privacy:** every note is a **hypothesis, needs human review**.
> A valid citation ID does not prove the claim follows from its evidence.
> Suggested commands are never executed. Redaction is bounded protection,
> not proof that a log contains no sensitive data. Keep traces and notes private.

## Prerequisites and setup

Start the default local lab first using the [local guide](../getting-started/local.md).
Have a working NVIDIA driver/CDI setup and enough GPU memory for the 3B model.
Doctor expects roughly 3.6 GiB free VRAM when AI is needed.
The supported setup uses rootless Podman, not GPU passthrough into kind.

Host driver, kernel and SELinux changes require approval. Use the
[RPM Fusion driver instructions](https://rpmfusion.org/Howto/NVIDIA) and
[NVIDIA CDI instructions](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/cdi-support.html)
for your host; doctor diagnoses missing driver, CDI or SELinux access.
Do not paste privileged install commands without review.

**Read-only host/network inspection.**

```sh
podman network inspect kind
```

**What you will see:** JSON describing the rootless kind network and subnets.
The network must already exist. Choose an unused address in that subnet for
the reference Quadlet's `Network=kind:ip=...`; its example address is not a
guaranteed free address on your host.

```sh
podman ps --format '{{.Names}}'
```

**What you will see:** running container names. Inspect their network addresses
before assigning a static one:

```sh
podman inspect --format '{{json .NetworkSettings.Networks}}' lab-zot
```

**What you will see:** the registry's network/address mapping, not its
environment or credentials. Repeat for the other attached containers.

> **Changes host user configuration:** copy the
> [reference Quadlet](../ollama.container) only if no existing Ollama unit would
> be overwritten. It pins the image digest, uses NVIDIA CDI and publishes
> `127.0.0.1:11434` only. It keeps models in the `ollama` Podman volume.
> Never publish port 11434 on all interfaces or remove that volume casually.

```sh
mkdir -p "$HOME/.config/containers/systemd"
cp docs/ollama.container "$HOME/.config/containers/systemd/ollama.container"
```

**What you will see:** no output on success; the reference unit is copied.
Edit its `Network=kind:ip=...` address to the verified free address before starting.
Keep its network alias, image digest, CDI device and loopback port unchanged.

```sh
systemctl --user daemon-reload
systemctl --user start ollama
```

**What you will see:** normally no output from either command. The user service
starts a container named `ollama`; a failure is shown by systemd.
`lab` does not manage or rewrite this host unit.

**Downloads model data and changes the model cache; no cloud inference.**

```sh
podman exec ollama ollama pull llama3.2:3b
```

**What you will see:** model-layer download progress and success, or a download
error. Review the [model terms](../licences.md#model-and-infrastructure-terms).
The model tag is mutable, unlike the pinned Ollama runtime image.

> **Changes the lab:** AI adds Langfuse, its storage services and restricted
> assistant identities. The engine also adds obs and secrets dependencies.
> Leave memory/build headroom; this is not just the model's VRAM footprint.

```sh
lab up --mode local --stacks ai
```

**What you will see:** the dependency plan and stack progress. Langfuse routes
appear after the apps reconcile. Ollama stays on the host; the cluster reaches
it through `ollama.ai.svc`.

```sh
lab doctor --mode local --profile full
```

**What you will see:** driver/CDI, VRAM, runtime digest, model, Langfuse and
deployed-app checks, plus the full-profile memory threshold. Fix failures.
This report does not change the configured profile or deploy the rest of full.

Export the public Gateway certificate using the
[local guide's certificate steps](../getting-started/local.md#3-check-the-sample-application).
Use `~/.lab/gateway.crt` below if the system does not already trust it.

## Investigate a firing alert

**Read-only workload evidence; local inference and a local Langfuse trace.**

```sh
lab assist alert KubePodCrashLooping --rules-first \
  --langfuse-ca "$HOME/.lab/gateway.crt"
```

**What you will see:** a JSON note, or `no firing alert` if it is not active.
If several alerts share that name, the command refuses to choose one.
Do not start a fault just to make an alert appear.

Select an exact alert when needed:

```sh
lab assist alert KubePodCrashLooping --namespace podinfo \
  --label pod=example-pod --rules-first --langfuse-ca "$HOME/.lab/gateway.crt"
```

**What you will see:** a note only for the matching firing alert, or a clear
missing/ambiguous-alert error. Replace `example-pod` with an actual alert label.
`--label key=value` is repeatable; conflicting selectors are rejected.

The note contains cited observations, a proposed cause, checks/actions and
completion/abstention/error information. Check each cited excerpt and timestamp.
A rules-first candidate is retained only when its deterministic evidence
pattern is present. A missing candidate or abstention is not a repair failure.

## Investigate an already-running drill

**Reference only; does not start, ack, verify or abort the drill.**

```sh
lab assist drill bad-deploy --rules-first --langfuse-ca "$HOME/.lab/gateway.crt"
```

**What you will see:** an incident note if that named drill is already running,
or `the named drill is not running`. The runner receives live alert/evidence,
not the exercise's cause, hints or solution. A human still diagnoses and repairs.

In the TUI, `a` on Drills investigates the active drill. Elsewhere, `a` opens
an alert picker; select with j/k and investigate with `a` or Enter.
Start the TUI with explicit certificate trust:

```sh
lab --langfuse-ca "$HOME/.lab/gateway.crt"
```

**What you will see:** Home, then the assistant pane when requested. It shows
collection progress, cited excerpts, rule candidate and note. Esc cancels a
running request; Esc again closes it. **Cancellation does not restore a drill.**
Use the drill's normal verify/abort workflow for restoration.

## Honest recorded eval result

The [recording in #40](https://github.com/chanupen/devops-lab/pull/40) used three
runs per case and mode. On five diagnosable drill fixtures:

| Model | Model-only cause accuracy | Rules-first cause accuracy | Rules-first cited-claim support |
| --- | --- | --- | --- |
| Llama 3.2 3B | **0/15 (0%)** | 15/15 (100%) | 60.0% |
| Qwen2.5 3B, research eval comparison only | **0/15 (0%)** | 15/15 (100%) | 64.2% |

**The rules were authored against these fixtures.** The 100% measures the
harness and plumbing, not generalisation or held-out root-cause accuracy.
It is the historical ten-case recording, not a new benchmark of the later
live-bad-deploy fixture. Expected abstentions are scored separately and are
not counted as wrong causal diagnoses. Claim support remains lower than cause
accuracy, so human review is required.

Qwen2.5 3B is non-commercial research/evaluation only, never the default
assistant or a bundled model. The eval CLI defaults to both model names;
explicitly select Llama for a Llama-only replay.

**Fixture replay, not fault injection.** It uses local Ollama and holds the
shared lab lock. Quit the mutating TUI first. No hosted endpoint is used.

```sh
lab assist eval --models llama3.2:3b --runs 3
```

**What you will see:** JSON diagnostics per run, per-case tables and aggregate
cause accuracy, abstentions, evidence support and timing for both modes.
Today's fixtures or model weights can differ from the historical recording;
do not promise the same scores.

Use a separate, sanitized fixture directory not used to author/tune rules
for a held-out comparison. Do not treat training-on-fixtures as independent
validation. **Do not add `--live`: it starts a real drill and restores it later.**

## Tool and inference boundaries

Tools can read bounded pod status, events, Loki logs, Prometheus samples and
Argo CD history. They cannot run a shell, exec into pods, read Secrets or
write Kubernetes resources. Dedicated identities restrict those tools.
Extra RBAC bindings can broaden an identity; preserve the intended grants.
Log/model output is data, not an instruction to the operator or runner.
Langfuse traces retain run/tool metadata, not evidence payloads, prompts,
notes or credentials. The incident note is shown in CLI/TUI output.

The receiver has an internal ClusterIP webhook, not a public Gateway route.
It and the TUI use local inference only. The CLI can enable hosted inference
only with explicit `--hosted`, endpoint/model flags and an API key from the
environment. That path sends selected cluster evidence off-host and may incur
charges. Obtain separate approval; never put the key in a command, document or Git.

Timeouts, missing causal context and invalid model output produce incomplete
notes/errors or abstentions, not invented evidence. Investigate manually using
[troubleshooting](troubleshooting.md). Never run a suggested fix without review.
