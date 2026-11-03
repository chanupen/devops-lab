# Lab 2: Build, scan and promote an image by retagging

A release should be the same bytes in every environment. In this lab you build an image, find known CVEs in it, fix them, and then promote the fixed image from dev to prod by copying its digest. Nothing is rebuilt for prod, so prod runs exactly the image you scanned.

The lab runs on your main lab's cluster, in the namespaces `lab02-dev` and `lab02-prod`. It needs the `registry` and `cluster` stacks (`lab up`). Images go to the lab registry, a writable namespace in the main lab's Zot, apart from its pull-through cache:

| From | Address |
|---|---|
| this host (push) | `127.0.0.1:5000/lab` |
| the cluster (pull) | `lab-zot:5000/lab` |

Zot listens on 127.0.0.1 only and speaks plain HTTP, so Podman needs `--tls-verify=false`. `lab doctor` checks that the host can push and the cluster can pull.

You need `podman`, `kubectl`, `jq` and `curl`. Trivy and Skopeo run as pinned images through rootless Podman, so there is nothing else to install:

```bash
trivy()  { podman run --rm --network host -v lab02-trivy-cache:/root/.cache ghcr.io/aquasecurity/trivy:0.76.1@sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c "$@"; }
skopeo() { podman run --rm --network host quay.io/skopeo/stable:v1.22.3@sha256:62da3f53e4b72219741bfa6681e092d30391a880b6346d20bf064778f7caa3c5 "$@"; }
```

Paste both lines into your shell first. `--network host` lets the containers reach the registry on 127.0.0.1. The volume `lab02-trivy-cache` keeps Trivy's vulnerability database between runs: the first scan downloads it, which takes about half a minute, and it uses about 1.6 GB of disk. `podman volume rm lab02-trivy-cache` frees it when you are done.

Check each step with `lab labs verify`. `lab labs hint` gives one hint at a time. `lab labs solution N` shows a reference answer.

## Step 1: Build the image with rootless Podman

Copy the app in `labs/02-image-promotion/app` to `~/lab02` and build it as `lab02-hello:0.1.0`. Do not change the Dockerfile yet.

The app is a tiny HTTP server: busybox `nc` answers on port 8080 as user 10001. Its base image, alpine 3.18.0, is old on purpose.

### Hints
- From the repo: `rm -rf ~/lab02 && cp -r labs/02-image-promotion/app ~/lab02`.
- `podman build -t lab02-hello:0.1.0 ~/lab02`. Rootless Podman builds as your user, with no daemon and no root.
- `podman image inspect lab02-hello:0.1.0 --format '{{.Config.User}}'` shows the user the image runs as.

### Expected result
`podman images` lists `localhost/lab02-hello` with tag `0.1.0`, and the image runs as user `10001`.

## Step 2: Push it to the lab registry as dev

Push the image to `127.0.0.1:5000/lab/lab02-hello` with the tag `dev`.

### Hints
- `podman push` takes the local image and a destination: `podman push --tls-verify=false lab02-hello:0.1.0 127.0.0.1:5000/lab/lab02-hello:dev`.
- The registry API lists the tags: `curl -s http://127.0.0.1:5000/v2/lab/lab02-hello/tags/list`.

### Expected result
The tag list shows `dev`. `skopeo inspect --tls-verify=false docker://127.0.0.1:5000/lab/lab02-hello:dev` prints a `Digest`.

## Step 3: Scan it with Trivy and fix it

Scan `dev` for HIGH and CRITICAL vulnerabilities. Then move the Dockerfile to the patched base, `docker.io/library/alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a`. Rebuild it as `lab02-hello:0.2.0`, scan it again, and push it as `dev`.

The base image is where most CVEs come from. Pinning it by digest means a rebuild uses exactly the base you checked.

### Hints
- `trivy image --image-src remote --insecure --severity HIGH,CRITICAL 127.0.0.1:5000/lab/lab02-hello:dev`. Look at the package names: busybox, OpenSSL, musl. All come from the base.
- Change only the `FROM` line in `~/lab02/Dockerfile`. Keep the digest: a tag such as `3.24.3` can be moved to other bytes, a digest cannot.
- `--exit-code 1` makes Trivy fail when it finds something, which is how a pipeline blocks a bad image. Scan the new `dev` with it.

### Expected result
The first scan lists a CRITICAL CVE in busybox (CVE-2022-48174) and HIGH CVEs in OpenSSL and musl. After the fix, Trivy finds no HIGH or CRITICAL CVE and exits 0. `dev` now points at the patched image.

New CVEs are published every day. If a later scan finds one in alpine 3.24.3 too, the fix is the same: a newer patched base.

## Step 4: Deploy dev by digest

Create the namespace `lab02-dev` and a Deployment `hello` in it. Its image is the dev image by digest: `lab-zot:5000/lab/lab02-hello@sha256:...`, not a tag.

A tag is a pointer anyone can move. A digest names exactly one image, so what the cluster runs cannot change under you.

### Hints
- Get the digest: `skopeo inspect --tls-verify=false --format '{{.Digest}}' docker://127.0.0.1:5000/lab/lab02-hello:dev`.
- The cluster pulls from `lab-zot:5000`, the registry's name on the kind network, not from 127.0.0.1.
- Give the pods the label `app=hello`. The image runs as a non-root user and listens on 8080: a readiness probe on `/` port 8080 tells you when it is up.

### Expected result
`kubectl -n lab02-dev get deployment hello -o jsonpath='{..image}'` shows `lab-zot:5000/lab/lab02-hello@sha256:...` with the dev digest, and the Deployment is available.

## Step 5: Promote to prod by copying the digest

Promote the image: copy the dev digest to the tag `prod` with Skopeo, without rebuilding. Then deploy it to the namespace `lab02-prod`, again by digest.

Rebuilding for prod would make a new image, even from the same Dockerfile, with a new digest: the image you tested is not the image you ship. Copying the digest ships the same bytes.

### Hints
- `skopeo copy --preserve-digests --src-tls-verify=false --dest-tls-verify=false docker://127.0.0.1:5000/lab/lab02-hello@<digest> docker://127.0.0.1:5000/lab/lab02-hello:prod`. `--preserve-digests` makes the copy fail rather than change the digest.
- After the copy, inspect `:prod`: its digest is the dev digest.
- The prod Deployment is the dev one with another namespace.

### Expected result
`prod` and `dev` have the same digest in the registry, and Deployment `hello` in `lab02-prod` runs `lab-zot:5000/lab/lab02-hello@` that digest.

## Step 6: Prove prod runs the digest you scanned

Prove that both environments run the image you scanned clean. Read the digest each pod actually runs, scan that digest, and record it on the prod Deployment as the annotation `lab.devops/scanned-digest`.

The Deployment says what should run. The pod's `imageID` says what the node actually pulled.

### Hints
- `kubectl -n lab02-prod get pods -l app=hello -o jsonpath='{.items[0].status.containerStatuses[0].imageID}'` shows the image with the digest the node pulled. Do the same in `lab02-dev`.
- Scan exactly that digest: `trivy image --image-src remote --insecure --severity HIGH,CRITICAL --exit-code 1 127.0.0.1:5000/lab/lab02-hello@<digest>`.
- `kubectl -n lab02-prod annotate deployment hello lab.devops/scanned-digest=<digest>`.

### Expected result
Both pods run the same digest, Trivy finds nothing HIGH or CRITICAL in it, and the prod Deployment carries it in `lab.devops/scanned-digest`.

`lab labs reset` deletes `lab02-dev`, `lab02-prod`, the `lab02-hello` images in the registry and in Podman. It keeps the Trivy cache volume `lab02-trivy-cache` and your `~/lab02` directory.
