# shellcheck shell=bash
# Variables here are used by the scripts that source this file.
# shellcheck disable=SC2034
# Shared by lab 2's verify scripts. Each script is read-only: it looks at
# Podman, the registry and the cluster, and exits 0 when the step is done,
# or prints why not as its last line and exits 1.
set -uo pipefail

fail() {
  echo "$*"
  exit 1
}

ok() {
  echo "$*"
  exit 0
}

# The lab registry: where the host pushes, and how the cluster pulls. lab
# sets both; these are the local defaults.
PUSH="${LAB_REGISTRY_PUSH:-127.0.0.1:5000/lab}"
PULL="${LAB_REGISTRY_PULL:-lab-zot:5000/lab}"
REPO=lab02-hello
API="http://${PUSH%%/*}/v2/${PUSH#*/}/$REPO"

# The first layer (uncompressed, so a push cannot change it) of each base.
VULN_BASE=sha256:bb01bd7e32b58b6694c8c3622c230171f1cec24001a82068a8d30d338f420d6c  # alpine 3.18.0
FIXED_BASE=sha256:75c137aedfb2760f88629236cb7b797630aed0c3d91d626d1d1bb694878fc2ba # alpine 3.24.3

ACCEPT="application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json"

need_cluster() {
  kubectl version --request-timeout=5s >/dev/null 2>&1 ||
    fail "the cluster does not answer (KUBECONFIG=${KUBECONFIG:-unset}): is the lab up? lab status"
}

# tag_digest <tag>: the manifest digest the registry has for the tag, or "".
tag_digest() {
  curl -fsS -m 10 -I -H "Accept: $ACCEPT" "$API/manifests/$1" 2>/dev/null |
    tr -d '\r' | awk -F': ' 'tolower($1)=="docker-content-digest" {print $2}'
}

# base_layer <tag-or-digest>: the image's first uncompressed layer (its base).
base_layer() {
  local cfg
  cfg=$(curl -fsS -m 10 -H "Accept: $ACCEPT" "$API/manifests/$1" | jq -r '.config.digest') || return 1
  curl -fsS -m 10 "$API/blobs/$cfg" | jq -r '.rootfs.diff_ids[0]'
}

# deployed <namespace>: the image of Deployment hello, or "".
deployed() {
  kubectl -n "$1" get deployment hello -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null
}

# ready <namespace>: Deployment hello has all its replicas available.
ready() {
  local want have
  want=$(kubectl -n "$1" get deployment hello -o jsonpath='{.spec.replicas}' 2>/dev/null)
  have=$(kubectl -n "$1" get deployment hello -o jsonpath='{.status.availableReplicas}' 2>/dev/null)
  [ -n "$want" ] && [ "${have:-0}" = "$want" ]
}

# running <namespace>: the digests of the images hello's pods run, one per line.
running() {
  kubectl -n "$1" get pods -l app=hello -o json 2>/dev/null |
    jq -r '.items[] | select(.status.phase=="Running") | .status.containerStatuses[].imageID | sub("^.*@"; "")' | sort -u
}

# Tools run as pinned images through rootless Podman, on the host network
# so they reach the registry on 127.0.0.1.
TRIVY_IMAGE=ghcr.io/aquasecurity/trivy:0.76.1@sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c
SKOPEO_IMAGE=quay.io/skopeo/stable:v1.22.3@sha256:62da3f53e4b72219741bfa6681e092d30391a880b6346d20bf064778f7caa3c5
FIXED_FROM=docker.io/library/alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
