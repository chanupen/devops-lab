# Lab 1: Bootstrap Argo CD from scratch

Argo CD keeps a cluster in line with what Git says. In this lab you install it on an empty cluster, deploy an app from Git, watch it undo a change made by hand, and then let one Application manage others.

You work on a cluster of your own. `lab labs start 01` creates it, and `lab labs stop` deletes it. Your main lab is not touched.

`lab labs start` prints a few progress lines and then the first step. Creating the cluster takes about a minute. Add `-v` to see kind's full output; it is always in the op log. Time counts only while your cluster runs. If the host reboots, run `lab labs start 01` again: it starts the cluster and the image cache, and you pick up at your step. `lab labs stop` deletes your cluster and the work on it, so it also resets your steps and time: the next `lab labs start 01` begins at step 1. It prints how far you got, for example "stopped at step 4/6 after 32m".

Before you start, you need `kubectl`, `helm`, `jq` and the `argocd` CLI on your PATH. `lab labs start` checks, and prints the install command for anything missing. To install the `argocd` CLI that matches this lab (v3.5.5, checksum verified):

```bash
mkdir -p ~/.local/bin && curl -fsSLo /tmp/argocd https://github.com/argoproj/argo-cd/releases/download/v3.5.5/argocd-linux-amd64 && echo "c3ddfa606fc3e89ab14f55bda6600b207c355b028f8eaabe69b817a4c4621aba  /tmp/argocd" | sha256sum -c - && install -m 0755 /tmp/argocd ~/.local/bin/argocd
```

On a Mac, `lab.yaml` has the `darwin/amd64` command.

Check each step with `lab labs verify`. `lab labs hint` gives one hint at a time. `lab labs solution N` shows a reference answer.

## Step 1: Install Argo CD with Helm

Install Argo CD into the `argocd` namespace with Helm. Use the `argo-cd` chart, version `10.10.5`, from the OCI registry `oci://ghcr.io/argoproj/argo-helm`.

Charts can come from an OCI registry, like images. The same chart is also in the classic Helm repository `https://argoproj.github.io/argo-helm`.

Always pin the chart version. Without a pin, the same command installs something different next month.

### Hints
- `helm install` takes an OCI reference as the chart: `oci://<registry>/<path>/<chart>`. You do not need `helm repo add`.
- Pin the version with `--version 10.10.5`. Create the namespace with `--create-namespace`.
- `--wait` makes Helm wait until the pods are Ready. Give it a few minutes with `--timeout 10m`.

### Expected result
`helm list -n argocd` shows a release of chart `argo-cd-10.10.5`. Every pod in `argocd` is Ready.

## Step 2: Log in with the argocd CLI

The Argo CD API is not exposed outside the cluster. Reach it with a port-forward, then log in with the `argocd` CLI as `admin`.

Use local port 18080. The main lab already uses 8080.

### Hints
- In a second terminal: `kubectl -n argocd port-forward svc/argocd-server 18080:443`. Keep it running.
- The first admin password is in the Secret `argocd-initial-admin-secret`, in the `password` key, base64-encoded.
- The server uses a self-signed certificate. Pass `--insecure` to `argocd login localhost:18080`.

### Expected result
`argocd account get-user-info` says `Logged In: true`.

## Step 3: Deploy an app from Git

Create an Application named `podinfo` in the `argocd` namespace. Its source is the Helm chart at path `charts/podinfo` in `https://github.com/stefanprodan/podinfo`, at tag `6.17.0`. Deploy it to the namespace `podinfo` in the same cluster. Then sync it.

An Application is a Kubernetes object. Write it as YAML and apply it with `kubectl`, so it can live in Git too.

### Hints
- The fields you need are `spec.source.repoURL`, `targetRevision` and `path`, and `spec.destination.server` and `namespace`. The cluster Argo CD runs in is `https://kubernetes.default.svc`.
- Add `CreateNamespace=true` under `spec.syncPolicy.syncOptions`, so Argo CD creates the `podinfo` namespace.
- A new Application is OutOfSync until it is synced: `argocd app sync podinfo`, then `argocd app wait podinfo --health`.

### Expected result
`argocd app get podinfo` shows Sync Status `Synced` and Health Status `Healthy`.

## Step 4: Turn on auto-sync

Make Argo CD sync `podinfo` on its own. Turn on self-heal, so it undoes changes made in the cluster. Turn on prune, so it deletes what was removed from Git.

### Hints
- These settings live under `spec.syncPolicy.automated` in the Application: `prune: true` and `selfHeal: true`.
- With the CLI: `argocd app set podinfo --sync-policy automated --auto-prune --self-heal`.

### Expected result
The Application's sync policy is automated, with prune and self-heal on.

## Step 5: Watch Argo CD undo drift

Change the cluster by hand: scale the `podinfo` Deployment to 3 replicas. Then watch the replica count.

Git says 1 replica, because that is the chart's default. With self-heal on, Argo CD puts it back within seconds. A change that is not in Git does not last.

### Hints
- `kubectl -n podinfo scale deployment podinfo --replicas=3`
- Watch it with `kubectl -n podinfo get deployment podinfo -w`. Press Ctrl+C to stop watching.
- To change the replica count for good, change it in Git, or in the Application's Helm values.

### Expected result
The Deployment was changed by hand, and it has 1 replica again.

## Step 6: Manage apps with an app of apps

Let one Application manage other Applications. Create an Application named `root` in the `argocd` namespace. Its source is the Helm chart at path `apps` in `https://github.com/argoproj/argocd-example-apps`, at commit `8088f4c0d970abb09e250248cc97e35623447cb5`. That chart makes one Application per entry in its `applications` value.

Keep two child apps, `guestbook` and `helm-guestbook`. Set them in the Application's Helm values, and pin the children to the same commit. Turn on auto-sync for `root`.

Now adding an app means adding an entry in Git. Nobody runs `kubectl apply` for it.

### Hints
- Set values inline with `spec.source.helm.valuesObject`. A list in your values replaces the chart's list.
- The chart reads the children's revision from `config.spec.source.targetRevision`.
- Give each entry a `destination: {namespace: <name>}`. The chart reads it, and fails on an entry without one ("nil pointer evaluating … .namespace").
- `helm-guestbook` is a Helm app: its entry needs `tool: {helm: {releaseName: helm-guestbook}}`.
- The children are named `example.guestbook` and `example.helm-guestbook`. Watch them with `argocd app list`.

### Expected result
`root` is Synced and manages two Applications, and both children are Synced.
