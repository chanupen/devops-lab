# 02 — Phase 1: local core

Needs: phase 0 merged, a read-only GitHub deploy key at `~/.ssh/devops-lab-deploy` (its public key added under the repo's Settings → Deploy keys, write access off). Gate: `lab up` from nothing to every app Synced and Healthy in under 5 minutes.

This phase was built as several PRs. Send them one at a time.

## 02a — state and engine

```text
internal/state and internal/engine.
- ~/.lab/state.json: atomic writes (temp file + rename), a flock on
  every update, an ops history (one log file per operation under
  ~/.lab/ops/, the CLI command that produced it).
- Single-instance lock: up, down and a TUI that can act take a
  non-blocking flock on ~/.lab/state.json.lab.lock. A second up/down
  exits at once with the holder (pid, terminal, since). Read-only
  commands (status, plan, doctor) never take it.
- Stack catalog with dependencies: registry -> cluster -> gitops ->
  core -> {obs, sample-app, ...}. Profiles minimal/standard/full plus
  extraStacks. Plan shows +, =, -, skip lines; a stack the provider
  can't run yet is "skip: not supported by the local provider yet",
  never a run-time failure. Apply in order, destroy in reverse,
  `lab up --stacks a,b` pulls in dependencies, `--prune` removes
  deployed stacks outside the profile.
- Engine is UI-agnostic: it streams progress messages the CLI prints
  and the TUI will render later.
Table-driven tests for ordering, planning, skip, prune and the lock.
```

## 02b — local provider: registry and cluster

```text
internal/provider/local: Zot and kind on rootless Podman.
- Talk to Podman through its libpod REST API on the user socket, not
  the podman CLI. kind through its Go library with
  KIND_EXPERIMENTAL_PROVIDER=podman.
- registry: Zot (pinned by digest) as container lab-zot on the kind
  network, a pull-through cache for docker.io, ghcr.io, quay.io,
  registry.k8s.io, with a named volume. Optional lab.yaml zot.addHosts
  passed as --add-host to lab-zot only.
- cluster: kind "lab", 1 control plane + 2 workers, node image picked
  by k8sVersion from a table of kind's pre-built images pinned BY DIGEST
  (kind's Podman provider pulls by digest, so the running version must
  be read from /kind/version in the node, never from the image name).
  containerd mirror hosts.toml files point every registry at lab-zot.
  The control plane publishes 30080/30443 on 127.0.0.1:8080/8443 only.
  Wait until every node is Ready. Kubeconfig at ~/.lab/kubeconfig.
- Every container lab makes is named lab-*.
Tests with a fake Podman runtime.
```

## 02c — gitops

```text
internal/gitops: Argo CD via the Helm SDK, then app-of-apps.
- Install Argo CD (chart pinned) with the Helm SDK, cache charts in
  ~/.lab/helm/charts (shared by every mode; prefetch before any stack
  runs; retries with backoff).
- Load repo.sshKeyFile as the repository Secret (never commit it).
  `lab up --stacks gitops --refresh-repo` reloads it.
- gitops/bootstrap: a Helm chart that renders one Argo CD Application
  per deployed stack (lab-bootstrap). Multi-source apps: the chart from
  its own repo, values from gitops/stacks/<stack>/ and
  gitops/overlays/<mode>/<stack>/.
- --revision <branch> points every app at a branch for testing; a plain
  `lab up` moves it back to main. lab status shows the revision.
- Waits: every app Synced and Healthy, with client-go informers.
```

## 02d — core, obs, sample-app

```text
Stacks as Argo CD apps.
- core: metrics-server, cert-manager (self-signed CA for localtest.me
  and *.localtest.me), Envoy Gateway with a Gateway "lab" in
  envoy-gateway-system on NodePorts 30080/30443, Argo CD's HTTPRoute at
  https://argocd.localtest.me:8443.
- obs: kube-prometheus-stack (24h retention, small requests), Grafana
  at grafana.localtest.me with an admin Secret generated in-cluster
  (never in Git), Loki single binary on filesystem storage, the
  OpenTelemetry Collector shipping pod logs to Loki.
- sample-app: podinfo frontend calling a backend, pulled through Zot,
  at podinfo.localtest.me; ServiceMonitors only when obs is there.
- `lab up` prints every HTTPRoute URL and the reminder to run
  `ssh -N fedora-lab` on the Mac. The Mac ssh config entry is in
  docs/design.md.
- lab.yaml default: profile minimal, extraStacks [obs].
Gate evidence: three runs of lab down + lab up from nothing, time to
all apps Synced and Healthy, and memory (podman stats on lab-*).
```
