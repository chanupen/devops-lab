# Lab 2: Build, scan and promote an image

Promote the bytes you verified, not another build with the same tag.

## Goal

Build, patch, scan and promote one image by digest. Allow 40 minutes;
intermediate level.

## Prerequisites

Local or cloud main cluster, with healthy `registry` and `cluster` stacks.
Tools: rootless Podman, kubectl, jq and curl. No other active lab/drill.
Start lab 2 and select its exports with [the lab lifecycle](../../docs/guides/labs.md).
Use a dedicated workspace such as `~/lab02`; do not overwrite previous work.
Append `--mode cloud` to every labs hint/verify/reset/stop command below for
a cloud session; the examples otherwise use the configured mode.

**Billed — approval required in cloud:** ECR stores the image and EKS runs it.
Build for `LAB_PLATFORM` (`linux/arm64` in cloud); keep ECR TLS enabled and use
a private auth file/password-stdin. Never print tokens or put them in arguments.
Local Zot is HTTP: disable registry TLS only for this local endpoint.

Define tool wrappers in the exercise shell, from a dedicated work directory:

```sh
trivy() { podman run --rm --network host -v lab02-trivy-cache:/root/.cache -v "$PWD":/work:Z ghcr.io/aquasecurity/trivy:0.76.1@sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c "$@"; }
skopeo() { podman run --rm --network host -v "$PWD":/work:Z quay.io/skopeo/stable:v1.22.3@sha256:62da3f53e4b72219741bfa6681e092d30391a880b6346d20bf064778f7caa3c5 "$@"; }
```

**What you will see:** no output until invoked. The wrappers mount only your
workspace; scanning needs a vulnerability database download and retains its
cache volume. Use Trivy on an exported image archive or the intended registry.

## Steps

1. [Build the original image](#step-1-build-the-image-with-rootless-podman).
2. [Push to development](#step-2-push-it-to-the-lab-registry-as-dev).
3. [Scan and patch](#step-3-scan-it-with-trivy-and-fix-it).
4. [Deploy by digest](#step-4-deploy-dev-by-digest).
5. [Promote without rebuilding](#step-5-promote-to-prod-by-copying-the-digest).
6. [Prove the running digest](#step-6-prove-prod-runs-the-digest-you-scanned).

## What you should see

The original base has findings. The patched image has no HIGH/CRITICAL findings
and both environments eventually run its scanned digest.

## Hints

```sh
lab labs hint
```

**What you will see:** the next hint for the current step.

## How to verify

```sh
lab labs verify
```

**What you will see:** a read-only check against Podman, registry and cluster.
Success advances the step. **Reference solutions (spoilers):** [solution scripts](solution/).

## Cleanup

**Destructive to the exercise; billed AWS deletion needs approval in cloud:**

```sh
lab labs reset
```

**What you will see:** removal of `lab02-dev`, `lab02-prod`, the exercise's
registry images and the two local `lab02-hello` image versions.
The host workspace and Trivy cache remain; volume removal needs separate approval.
Use `--mode cloud` for cloud cleanup.

```sh
lab labs stop
```

**What you will see:** the session ends. Stop alone would leave main-cluster work.
Cloud stop does not end EKS/ECR billing; finish the approved down/leftovers sequence.

## Step 1: Build the image with rootless Podman

Copy the provided `app/` into your dedicated workspace. Build its original Dockerfile for `LAB_PLATFORM` as `localhost/lab02-hello:0.1.0`. Run it with rootless Podman to confirm its response.

### Hints
- Keep the original Alpine base for this first build; it is intentionally old.
- Any test listener must bind to loopback. Do not modify the repository fixture.

### Expected result
The local `0.1.0` image exists for `LAB_PLATFORM`, keeps user 10001 and the original Alpine 3.18.0 base, and serves the provided hello response.

## Step 2: Push it to the lab registry as dev

Push the original image as `$LAB_REGISTRY_PUSH/lab02-hello:dev`. Record the registry manifest digest, not only the tag.

### Hints
- Push and pull prefixes differ locally; use the exported host push prefix.
- A registry's manifest digest identifies the uploaded bytes.

### Expected result
The dev registry tag resolves to the original image digest.

## Step 3: Scan it with Trivy and fix it

Scan for HIGH and CRITICAL findings. Update the base to `docker.io/library/alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a`, build `localhost/lab02-hello:0.2.0`, scan it again, then push that image as dev. Retain the scanned digest as evidence.

### Hints
- The pinned base is a multi-platform reference; build for the exported target.
- A scanner's exit status depends on its severity/exit-code options.
- An image archive lets the containerized scanner inspect a local build without sharing a runtime socket.

### Expected result
The patched dev image has the expected Alpine 3.24.3 base and no HIGH/CRITICAL findings in the scan.

## Step 4: Deploy dev by digest

Deploy `hello` in namespace `lab02-dev` using the pull prefix and patched digest. Expose it with a Service inside the cluster.

### Hints
- A tag can move after scanning. The Deployment's image must contain the digest.
- Read both the desired image reference and the pod's actual imageID.

### Expected result
The dev Deployment is available and its pods run the patched registry digest.

## Step 5: Promote to prod by copying the digest

Copy the dev manifest to the prod registry tag without rebuilding. Deploy `hello` in `lab02-prod` using that same digest and pull prefix.

### Hints
- Skopeo can copy a registry manifest while preserving its digest.
- Promotion changes the environment's reference, not the image contents.

### Expected result
Dev and prod registry tags have the same digest; both Deployments are available.

## Step 6: Prove prod runs the digest you scanned

Record the digest you scanned on prod Deployment `hello` as `lab.devops/scanned-digest`. Compare it with every running hello pod in both namespaces.

### Hints
- A Deployment metadata annotation is distinct from its pod-template annotations.
- Compare registry digest, desired reference and actual pod imageID.

### Expected result
Dev and prod pods run the annotated scanned digest, backed by the patched base.
