# Lab 5: Kyverno policy that blocks images without a digest

A tag such as `6.15.0` is a pointer: whoever can push can move it to other bytes. A digest names exactly one image. In this lab you write a Kyverno policy that refuses Pods whose images are not pinned by digest, see it deny and allow, grant one named workload an exception, read the reports Kyverno writes, and test the policy offline before it ever reaches a cluster.

The lab runs on your main lab's cluster, in the namespace `lab05-apps`. It needs the `policy` stack: `lab up --stacks policy` installs Kyverno and the Pod Security Standards "baseline" policies, in Audit mode. Your policy is scoped to `lab05-*` namespaces, so nothing else in the cluster is affected. Cluster-scoped objects you create carry the label `lab.devops/lab=05-kyverno-digest`, which is how `lab labs reset` finds them.

You need `kubectl`, `jq` and `podman`. The kyverno CLI runs as a pinned image through rootless Podman:

```bash
kyverno() { podman run --rm --network none -v "$PWD":/work:Z -w /work ghcr.io/kyverno/kyverno-cli:v1.19.2@sha256:322ec5044d600930cb3af0e56dc33154a2d1d1fba58f4cadad3f47be7e78b11c "$@"; }
```

The image is `ghcr.io/stefanprodan/podinfo:6.15.0`, by digest `@sha256:ec73780a8425f59ea49f5bc8cdff0d598805a224fbaa1f86c67a244f250fa9da`.

You write the policy as a `ValidatingPolicy` (`policies.kyverno.io/v1`): rules in CEL, the expression language of Kubernetes' own ValidatingAdmissionPolicy. The baseline policies of the stack are written the same way.

Many clusters still run the older form, the `kyverno.io/v1` ClusterPolicy: rules with `match`, `validate.pattern` and `failureAction`, and wildcards such as `image: "*@sha256:*"` instead of CEL. Kyverno 1.19 still runs it but marks it deprecated, and `kubectl` prints a warning for it. Expect to read ClusterPolicies, and to move them to ValidatingPolicy: [the migration guide](https://kyverno.io/docs/guides/migration-to-cel/) maps one onto the other.

Check each step with `lab labs verify`. `lab labs hint` gives one hint at a time. `lab labs solution N` shows a reference answer.

## Step 1: Write a ValidatingPolicy that requires digests

Write a ValidatingPolicy `require-image-digest` with the label `lab.devops/lab: 05-kyverno-digest`. It matches Pods (create and update), only in namespaces `lab05-*`, and denies a Pod unless every container image contains `@sha256:`.

### Hints
- `spec.matchConstraints.resourceRules` says what to check: `apiGroups: [""]`, `apiVersions: [v1]`, `operations: [CREATE, UPDATE]`, `resources: [pods]`. `spec.validationActions: [Deny]` blocks; `[Audit]` would only report.
- Scope it with a `matchConditions` entry, a CEL expression on the object: `has(object.metadata.namespace) && object.metadata.namespace.startsWith('lab05-')`.
- The check is one CEL expression in `spec.validations`, with a `message`: `object.spec.containers.all(c, c.image.contains('@sha256:'))`, and the same for `initContainers` when there are any (`!has(object.spec.initContainers) || ...`). Kyverno adds the same check for Deployments, Jobs and the other Pod controllers itself (autogen), so a bad Deployment fails at once instead of making Pods that fail.

### Expected result
`kubectl get vpol require-image-digest` shows it ready, and `kubectl get vpol require-image-digest -o jsonpath='{.status.autogen}'` lists the checks Kyverno generated for Deployments and Jobs.

## Step 2: Show that a tag-only Pod is denied

Create the namespace `lab05-apps`. Then try a Pod with the image by tag only, `ghcr.io/stefanprodan/podinfo:6.15.0`, with a server-side dry run.

A server-side dry run sends the object through the API server and its admission webhooks, Kyverno's included, and stores nothing. It is how a pipeline checks a manifest against the real cluster's policies.

### Hints
- `kubectl -n lab05-apps run tagged --image=ghcr.io/stefanprodan/podinfo:6.15.0 --restart=Never --dry-run=server`.
- `--dry-run=client` would not reach Kyverno: it never leaves your machine.
- Try the same Pod in `default`: it is allowed, because the policy only applies to `lab05-*`.

### Expected result
The dry run fails with `admission webhook "vpol.validate.kyverno.svc-fail-finegrained-require-image-digest" denied the request` and your message.

## Step 3: Show that a digest Pod is allowed

Run a Pod `web` in `lab05-apps` with the same image by digest.

### Hints
- `kubectl -n lab05-apps run web --restart=Never --image=ghcr.io/stefanprodan/podinfo:6.15.0@sha256:ec73780a...` (the full digest is above).
- A reference may have both a tag and a digest. The tag is then only a label for people; the digest decides what is pulled.

### Expected result
Pod `web` is Running.

## Step 4: Grant one named workload an exception

A legacy app cannot move to digests yet. Let exactly one workload, a Deployment `legacy` in `lab05-apps`, run the tag-only image. Write a PolicyException `legacy-tag` in `lab05-apps` for it, then create the Deployment. Every other tag-only Pod must still be denied, even one that copies the Deployment's labels.

An exception should be as narrow as you can make it: one policy, one named workload. Exceptions are reviewed like code, so they live in Git, next to the policy.

### Hints
- A PolicyException (`policies.kyverno.io/v1`) names the policy in `policyRefs` (`name` and `kind: ValidatingPolicy`) and says what it covers in `matchConditions`, CEL expressions on the object.
- The policy checks the Deployment, its ReplicaSet and its Pods. Match the Deployment by kind and name, and its children by who owns them: `object.metadata.ownerReferences.exists(o, o.kind == 'Deployment' && o.name == 'legacy')` for the ReplicaSet, and a ReplicaSet owner named `legacy-...` for the Pods.
- Matching on a label such as `app=legacy` is too wide: anyone can copy a label. Create the exception before the Deployment, or the Deployment is denied. Kyverno takes a few seconds to load a new exception: if the Deployment is denied right after, try again.

### Expected result
Deployment `legacy` is available with the tag-only image. A dry run of another tag-only Pod in `lab05-apps` is still denied, and so is one labelled `app=legacy`.

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
- `kyverno-test.yaml` is `apiVersion: cli.kyverno.io/v1alpha1`, `kind: Test`, with `policies`, `resources` and `results`. Each result names the `policy`, `kind`, `resources` and the expected `result`, with `isValidatingPolicy: true` (a ValidatingPolicy has no rule names).
- Run it with the function above: `cd ~/lab05/test && kyverno test .`.
- A test that passes says your expectations hold, not that the policy is right: write a result for each case that matters, the denials included.

### Expected result
`Test Summary: 2 tests passed and 0 tests failed`.

`lab labs reset` deletes `lab05-apps`, with every Pod, Deployment, PolicyException and PolicyReport in it, and the ValidatingPolicy with the lab's label. It keeps your `~/lab05` directory.
