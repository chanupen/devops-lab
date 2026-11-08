# Choose profiles and stacks

[Docs index](../README.md) · [CLI reference](cli.md) · [Architecture](../concepts/architecture.md)

A profile requests a set of stacks. The engine adds dependencies and orders
them. A stack is a named platform capability, not a separate cluster.
The checked-in default is `minimal` plus `extraStacks: [obs]`.

## Profiles

| Profile | Requested stacks, before dependency expansion | Memory guidance |
| --- | --- | --- |
| `minimal` | registry, cluster, gitops, core, sample-app | Doctor threshold 4 GiB; default plus obs measured about 4.6 GiB |
| `standard` | minimal plus secrets, obs, policy, scaling, sample-db | Doctor threshold 12 GiB; keep host/build headroom |
| `full` | standard plus ci, chaos, ai | Doctor threshold 19 GiB; GPU/Ollama required for AI |

The currently deployable local full profile measured 7.47 GiB with Ollama
idle. That includes its selected deployable stacks, not unsupported CI,
chaos or scaling. It is not a promise that a future complete full profile
fits in that memory. GPU model memory and host/build headroom are separate.

Change `profile` and `extraStacks` in the existing `lab.yaml`; preserve its
other fields. For example:

```yaml
profile: minimal
extraStacks: [obs]
```

Doctor's `--profile` selects a readiness threshold; it does not change
`lab.yaml` or deploy a profile. There is no up `--profile` flag.

## Stack catalog

| Stack | Direct dependencies | What it supplies | Current availability |
| --- | --- | --- | --- |
| registry | none | Zot/cache locally; ECR in AWS | Both |
| cluster | registry | kind or EKS, CNI and storage | Both |
| gitops | cluster | Argo CD by default; Flux alternative | Both |
| core | gitops | metrics-server, Envoy Gateway, cert-manager | Both |
| secrets | gitops | External Secrets Operator and Secrets Manager integration | Both; local Moto is provider-managed |
| obs | gitops, core | Prometheus, Grafana, Loki, Tempo, OTel and SLO tooling | Both |
| policy | gitops | Kyverno | Both |
| scaling | gitops, obs | Intended KEDA/Karpenter exercise stack | Skipped; cloud Karpenter is installed during cluster bootstrap, not this stack |
| ci | gitops, registry, secrets | Intended ARC/Jenkins stack | Skipped |
| chaos | obs | Intended Chaos Mesh stack | Skipped |
| ai | obs, secrets | Host Ollama access, Langfuse and tracing dependencies | Local only; cloud stub is skipped |
| sample-app | core, registry | podinfo sample application | Both |
| sample-db | core, secrets, registry | Database-backed app, Postgres and TLS | Both |

Unsupported stacks are skipped at plan time, not failed during apply. A skip
does not mean they are deployed or measured. Never install a second GitOps
controller or manually deploy skipped stacks to hide the warning.

## Inspect before changing

**Read-only.**

```sh
lab plan --mode local
```

**What you will see:** requested stacks in dependency order and counts.
`+` applies; `=` keeps; `-` destroys or skips. Skips include `skip:` in the reason.

```sh
lab doctor --mode local --profile minimal
```

**What you will see:** host/readiness checks for the chosen threshold.
Warnings need review; failures block a clean start. See
[the reading guide](../getting-started/local.md#read-lab-doctor).

## Add a capability

> **Changes the lab:** the next command adds stacks to the running lab.
> It is not a dry run. Cloud equivalents are billed and require the approved
> [cloud sequence](../getting-started/cloud.md), not blind substitution of modes.

```sh
lab up --mode local --stacks obs
```

**What you will see:** a plan including dependencies, then progress for missing
or failed stacks. Applied stacks remain. `--stacks` adds to the profile and
retains deployed stacks; it does not replace the profile or make an isolated lab.
Persist desired additions in `extraStacks` for future recreations.

Exercise requirements belong to each lab/drill. The default omits `secrets`
and `sample-db`; do not assume every drill is ready because obs is healthy.

## Remove a capability

> **Destructive:** removing a dependency also removes its deployed dependents.
> Stack data can be lost. Review the preview before confirming.

```sh
lab plan --mode local --destroy
```

**What you will see:** the whole-lab teardown preview in reverse order.
Nothing is deleted. There is no partial `plan --stacks` flag; the following
selected down prints its own plan before confirmation.

```sh
lab down --mode local --stacks sample-db
```

**What you will see:** the destruction plan and a confirmation prompt, then
teardown progress if approved. Remove it from `extraStacks` too if it should
not return on the next up. A profile that still requests it will recreate it.

Whole-lab teardown is in the getting-started guides. The Zot image cache is
retained by default; volume purge needs separate approval.

## Config, revision and mode

Run from the checkout, or provide `--config`. Discovery checks `./lab.yaml`
before `~/.lab/lab.yaml`. Local and cloud have separate state/kubeconfigs
but share one mutation lock.

Plain up tracks `main`. A testing `--revision` selects a GitOps revision; it
does not select a local source build or alter the AWS region.
Return GitOps to `main` before deleting a tested branch.
The local `k8sVersion` pin does not select EKS's version; cloud uses its
compatibility table and optional `cloud.k8sVersion`.

Cloud defaults to a 2h TTL, capped at 8h ahead. Budgets and TTL are not hard
spending caps. Do not extend without fresh approval.
