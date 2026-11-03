# Lab 5: Kyverno policy that blocks images without a digest

A tag such as `6.15.0` is a pointer: whoever can push can move it to other bytes. A digest names exactly one image. In this lab you write a Kyverno policy that refuses Pods whose images are not pinned by digest, see it deny and allow, grant one named workload an exception, read the reports Kyverno writes, and test the policy offline before it ever reaches a cluster.

The lab runs on your main lab's cluster, in the namespace `lab05-apps`. It needs the `policy` stack: `lab up --stacks policy` installs Kyverno and the Pod Security Standards "baseline" policies, in Audit mode. Your policy is scoped to `lab05-*` namespaces, so nothing else in the cluster is affected. Cluster-scoped objects you create carry the label `lab.devops/lab=05-kyverno-digest`, which is how `lab labs reset` finds them.

You need `kubectl`, `jq` and `podman`. The kyverno CLI runs as a pinned image through rootless Podman:

```bash
kyverno() { podman run --rm --network none -v "$PWD":/work:Z -w /work ghcr.io/kyverno/kyverno-cli:v1.19.2@sha256:322ec5044d600930cb3af0e56dc33154a2d1d1fba58f4cadad3f47be7e78b11c "$@"; }
```

The image is `ghcr.io/stefanprodan/podinfo:6.15.0`, by digest `@sha256:ec73780a8425f59ea49f5bc8cdff0d598805a224fbaa1f86c67a244f250fa9da`.

Kyverno 1.19 marks the `kyverno.io/v1` ClusterPolicy kind as deprecated, in favour of CEL-based `ValidatingPolicy`. It still works, and most policies in use today are written this way; `kubectl` prints a warning about it. The baseline policies of the stack are already `ValidatingPolicy`.

Check each step with `lab labs verify`. `lab labs hint` gives one hint at a time. `lab labs solution N` shows a reference answer.

## Step 1: Write a ClusterPolicy that requires digests

Write a ClusterPolicy `require-image-digest` with the label `lab.devops/lab: 05-kyverno-digest`. One rule, `images-by-digest`, matches Pods in namespaces `lab05-*` and requires every container image to match `*@sha256:*`. Make it `Enforce`, so a bad Pod is denied, not only reported.

### Hints
- A rule has `match` (here `any: [{resources: {kinds: [Pod], namespaces: ["lab05-*"]}}]`) and `validate`, with `failureAction`, `message` and a `pattern`.
- The pattern mirrors the Pod: `spec.containers` is a list, and `- image: "*@sha256:*"` checks every entry. `=(initContainers)` checks init containers only when there are any.
- You only write the rule for Pods. Kyverno adds the same rule for Deployments, Jobs and the other Pod controllers itself (autogen, `autogen-images-by-digest`), so a bad Deployment fails at once instead of making Pods that fail.

### Expected result
`kubectl get clusterpolicy require-image-digest` shows it `READY` `True`.

## Step 2: Show that a tag-only Pod is denied

Create the namespace `lab05-apps`. Then try a Pod with the image by tag only, `ghcr.io/stefanprodan/podinfo:6.15.0`, with a server-side dry run.

A server-side dry run sends the object through the API server and its admission webhooks, Kyverno's included, and stores nothing. It is how a pipeline checks a manifest against the real cluster's policies.

### Hints
- `kubectl -n lab05-apps run tagged --image=ghcr.io/stefanprodan/podinfo:6.15.0 --restart=Never --dry-run=server`.
- `--dry-run=client` would not reach Kyverno: it never leaves your machine.
- The denial names the policy, the rule and your message.

### Expected result
The dry run fails with `admission webhook "validate.kyverno.svc-fail" denied the request` and your message.

## Step 3: Show that a digest Pod is allowed

Run a Pod `web` in `lab05-apps` with the same image by digest.

### Hints
- `kubectl -n lab05-apps run web --restart=Never --image=ghcr.io/stefanprodan/podinfo:6.15.0@sha256:ec73780a...` (the full digest is above).
- A reference may have both a tag and a digest. The tag is then only a label for people; the digest decides what is pulled.

### Expected result
Pod `web` is Running.

## Step 4: Grant one named workload an exception

A legacy app cannot move to digests yet. Let exactly one workload, a Deployment `legacy` in `lab05-apps`, run the tag-only image. Write a PolicyException `legacy-tag` in `lab05-apps` for it, then create the Deployment. Every other tag-only Pod must still be denied.

An exception should be as narrow as you can make it: one policy, the rules it needs, one named workload. Exceptions are reviewed like code, so they live in Git, next to the policy.

### Hints
- A PolicyException (`kyverno.io/v2`) lists `exceptions` (a `policyName` and its `ruleNames`) and a `match`, like a policy rule.
- The Deployment is checked by the autogen rule, its Pods by `images-by-digest`. Name both rules, and match the Deployment, its ReplicaSet and its Pods: `names: [legacy, legacy-*]`.
- Create the exception before the Deployment, or the Deployment is denied.

### Expected result
Deployment `legacy` is available with the tag-only image, and a dry run of another tag-only Pod in `lab05-apps` is still denied.

## Step 5: Read the PolicyReports

The baseline policies do not block: they are in Audit mode. Run a Pod `debug` in `lab05-apps` with the image by digest and a `hostPath` volume (for example `/tmp`, read-only). Then find the PolicyReport that says what is wrong with it.

Audit is how you roll out a new policy: first see what it would deny, fix that, then switch it to Enforce.

### Hints
- `kubectl -n lab05-apps get policyreports` lists one report per resource. `-o wide` adds pass and fail counts.
- `kubectl -n lab05-apps get policyreport <name> -o yaml` shows each `result`: the policy, the rule, `pass` or `fail`, and a message.
- Your policy appears in the reports too: Pod `web` passes `require-image-digest`.

### Expected result
Pod `debug` runs, and its report has `disallow-host-path` with `result: fail`.

## Step 6: Test the policy offline with kyverno test

Before a policy reaches a cluster, test it. In `~/lab05/test`, put the policy (`policy.yaml`), two Pods in `lab05-apps` (`resources.yaml`: `tagged` by tag, `pinned` by digest) and a `kyverno-test.yaml` that expects `tagged` to fail the rule and `pinned` to pass. Run `kyverno test` on it.

`kyverno test` needs no cluster, so a pipeline can run it on every change to a policy.

### Hints
- `kyverno-test.yaml` is `apiVersion: cli.kyverno.io/v1alpha1`, `kind: Test`, with `policies`, `resources` and `results`. Each result names the `policy`, `rule`, `kind`, `resources` and the expected `result`.
- Run it with the function above: `cd ~/lab05/test && kyverno test .`.
- A test that passes says your expectations hold, not that the policy is right: write a result for each case that matters, the denials included.

### Expected result
`Test Summary: 2 tests passed and 0 tests failed`.

`lab labs reset` deletes `lab05-apps`, with every Pod, Deployment, PolicyException and PolicyReport in it, and the ClusterPolicy with the lab's label. It keeps your `~/lab05` directory.
