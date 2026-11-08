# Lab 1: Bootstrap Argo CD from scratch

Argo CD keeps a cluster in line with what Git says. In this lab you install it on an empty cluster, deploy an app from Git, watch it undo a change made by hand, and then let one Application manage others.

## Goal

Install Argo CD, deploy from Git, observe self-healing and build an app-of-apps.
Allow about 45 minutes; beginner level.

## Prerequisites

Local only. Rootless Podman and kind ready; `kubectl`, `helm`, `argocd` and `jq`
on PATH. Start creates an isolated single-node scratch cluster, not your main lab.
Use [the lab lifecycle](../../docs/guides/labs.md) to start lab 1 and select
its private kubeconfig. No other lab or drill may be active.

Start prints the platform-specific installer for missing tools. This Linux
amd64 Argo CD v3.5.5 installer matches the metadata and verifies its checksum:

```sh
mkdir -p ~/.local/bin && curl -fsSLo /tmp/argocd https://github.com/argoproj/argo-cd/releases/download/v3.5.5/argocd-linux-amd64 && echo "c3ddfa606fc3e89ab14f55bda6600b207c355b028f8eaabe69b817a4c4621aba  /tmp/argocd" | sha256sum -c - && install -m 0755 /tmp/argocd ~/.local/bin/argocd
```

**What you will see:** checksum `OK`, then installation in your user bin.
**Writes host files:** review `/tmp/argocd` and the target first; do not overwrite
another user's files. The metadata has a separate Intel macOS command.

## Steps

1. [Install Argo CD](#step-1-install-argo-cd-with-helm).
2. [Log in](#step-2-log-in-with-the-argocd-cli).
3. [Deploy from Git](#step-3-deploy-an-app-from-git).
4. [Enable reconciliation](#step-4-turn-on-auto-sync).
5. [Observe drift](#step-5-watch-argo-cd-undo-drift).
6. [Manage child applications](#step-6-manage-apps-with-an-app-of-apps).

Detailed tasks follow the lifecycle sections. Full command answers stay in
the labelled reference scripts.

## What you should see

A separate cluster with Argo CD and healthy Applications. Manual drift is
reconciled from Git. The main lab's Applications are unchanged.

## Hints

```sh
lab labs hint
```

**What you will see:** the next hint for your current step.

## How to verify

```sh
lab labs verify
```

**What you will see:** a read-only step result; success advances progress.
Use a step number to recheck earlier work. **Reference solutions (spoilers):**
[solution scripts](solution/), or `lab labs solution N`, which prints an answer.

## Cleanup

**Destructive to this scratch lab:**

```sh
lab labs stop
```

**What you will see:** the scratch cluster is deleted and its steps/time reset.
To retry inside the existing cluster instead, use `lab labs reset`; it removes
Argo CD, its CRDs and the exercise Applications/namespaces. Stop manual
port-forwards with Ctrl-C. A reboot pauses scratch time; starting the same lab
again resumes it, but stop discards its cluster work.

## Step 1: Install Argo CD with Helm

Install the `argo-cd` chart version `10.10.5` from `oci://ghcr.io/argoproj/argo-helm` into namespace `argocd`. Pin the version and wait for readiness.

### Hints
- Helm supports OCI chart references without adding a classic chart repository.
- Check the install flags for namespace creation, version and readiness timeout.

### Expected result
The release uses chart `argo-cd-10.10.5` and all Argo CD pods are Ready.

## Step 2: Log in with the argocd CLI

Reach the scratch Argo CD API on loopback port 18080 and authenticate as admin. Do not expose it on the LAN or reuse the main lab's port 8080.

### Hints
- In a second terminal: use a port-forward bound to `127.0.0.1`.
- The initial admin credential is in the scratch namespace's initial-admin Secret.
- The API uses a self-signed certificate. Review trust options before login; any insecure exception must be limited to this isolated loopback exercise. Never print the password in shared logs or put it in shell history.

### Expected result
The argocd CLI reports `Logged In: true` for the scratch server.

## Step 3: Deploy an app from Git

Create Application `podinfo` in `argocd`, using `https://github.com/stefanprodan/podinfo`, revision `6.17.0`, path `charts/podinfo`. Deploy into namespace `podinfo` on this cluster and sync it.

### Hints
- An Application connects a source/revision/path with a destination cluster/namespace.
- Keep its manifest in your exercise workspace; do not change the main bootstrap.

### Expected result
Application `podinfo` is Synced and Healthy; its podinfo Deployment is available.

## Step 4: Turn on auto-sync

Enable automated sync, pruning and self-healing on Application `podinfo`. Explain the difference between a new Git revision, a removed Git object and a manual change in the cluster.

### Hints
- Inspect the Application's automated sync policy fields.
- Pruning and self-healing are separate choices, not implied by automated sync.

### Expected result
The Application has automated sync with both prune and selfHeal enabled.

## Step 5: Watch Argo CD undo drift

Record the Git-desired replica count, change the live podinfo Deployment's replicas, then observe Argo CD restoring the desired value.

### Hints
- Compare Application sync status and Deployment state over time.
- Do not change Git or disable self-heal to keep the manual replica change.

### Expected result
The Deployment returns to Git's desired replica count and the Application is Synced.

## Step 6: Manage apps with an app of apps

Create root Application `root` from `https://github.com/argoproj/argocd-example-apps`, revision `8088f4c0d970abb09e250248cc97e35623447cb5`, path `apps`. Its destination is `argocd`; sync it and then its child Applications. Configure automated sync and its Helm values to manage exactly `guestbook` and `helm-guestbook`, not every example in the source repository.

### Hints
- The root's source contains Applications rather than the workloads themselves.
- Check each child's destination namespace and health, not only the root.

### Expected result
Root has automated sync and manages only `guestbook` and `helm-guestbook`; both are Synced and Healthy with available Deployments in their namespaces.
