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
# sets both; these are the local defaults. Locally it is Zot over plain
# HTTP with no login; in cloud mode it is ECR over HTTPS, and its password
# is a 12-hour token from the aws CLI (need_registry).
PUSH="${LAB_REGISTRY_PUSH:-127.0.0.1:5000/lab}"
PULL="${LAB_REGISTRY_PULL:-lab-zot:5000/lab}"
REPO=lab02-hello
REG=${PUSH%%/*}
case "$REG" in
  *.amazonaws.com) ECR=1 SCHEME=https TLS=true ;;
  *) ECR="" SCHEME=http TLS=false ;;
esac
API="$SCHEME://$REG/v2/${PUSH#*/}/$REPO"
REG_PASS=""

# The platform the cluster's nodes run: the host's (amd64) locally, arm64
# (Graviton) in cloud mode. Images are built for it.
PLATFORM="${LAB_PLATFORM:-linux/amd64}"

# The first layer (uncompressed, so a push cannot change it) of each base,
# for the platform.
case "$PLATFORM" in
  linux/arm64)
    VULN_BASE=sha256:94dd7d531fa5695c0c033dcb69f213c2b4c3b5a3ae6e497252ba88da87169c3f  # alpine 3.18.0
    FIXED_BASE=sha256:a278e6f39431cd0ed74238a3d377893ad0d905d68b676bf9a65529080669756b # alpine 3.24.3
    ;;
  *)
    VULN_BASE=sha256:bb01bd7e32b58b6694c8c3622c230171f1cec24001a82068a8d30d338f420d6c  # alpine 3.18.0
    FIXED_BASE=sha256:75c137aedfb2760f88629236cb7b797630aed0c3d91d626d1d1bb694878fc2ba # alpine 3.24.3
    ;;
esac

ACCEPT="application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json"

need_cluster() {
  kubectl version --request-timeout=5s >/dev/null 2>&1 ||
    fail "the cluster does not answer (KUBECONFIG=${KUBECONFIG:-unset}): is the lab up? lab status"
}

# The aws CLI, as a pinned image through rootless Podman, with the keys and
# region lab sets (cloud mode only: ECR's login and lab 2's reset).
AWS_IMAGE=docker.io/amazon/aws-cli:2.38.3@sha256:efe32e825cf203652eaca3e7e94679580274e7d567c39270aaa36a5144737108
aws() { podman run --rm --network host -e 'AWS_*' "$AWS_IMAGE" "$@"; }

# need_registry: the registry answers; on ECR it also sets REG_PASS. Call it
# at the top level of a script, not in $(...), so REG_PASS stays set.
need_registry() {
  if [ -n "$ECR" ]; then
    REG_PASS=$(aws ecr get-login-password 2>/dev/null) && [ -n "$REG_PASS" ] ||
      fail "no ECR login: the aws CLI could not get a token (lab cloud login, then lab labs verify again)"
  fi
  rcurl -fsS -m 10 -o /dev/null "$SCHEME://$REG/v2/" ||
    fail "the lab registry does not answer on $REG: lab doctor checks it"
}

# rcurl: curl against the registry API, with ECR's login when there is one.
# ECR sends blobs from S3 through a redirect; -L follows it, and curl sends
# the login to the registry host only.
rcurl() {
  if [ -n "$ECR" ]; then
    curl -L -u "AWS:$REG_PASS" "$@"
  else
    curl -L "$@"
  fi
}

# registry_login: podman push needs a login to ECR; Zot needs none.
registry_login() {
  [ -z "$ECR" ] || printf '%s\n' "$REG_PASS" | podman login -u AWS --password-stdin "$REG" >/dev/null
}

# tag_digest <tag>: the manifest digest the registry has for the tag, or "".
tag_digest() {
  rcurl -fsS -m 10 -I -H "Accept: $ACCEPT" "$API/manifests/$1" 2>/dev/null |
    tr -d '\r' | awk -F': ' 'tolower($1)=="docker-content-digest" {print $2}'
}

# base_layer <tag-or-digest>: the image's first uncompressed layer (its base).
base_layer() {
  local cfg
  cfg=$(rcurl -fsS -m 10 -H "Accept: $ACCEPT" "$API/manifests/$1" | jq -r '.config.digest') || return 1
  rcurl -fsS -m 20 "$API/blobs/$cfg" | jq -r '.rootfs.diff_ids[0]'
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

# trivy and skopeo with the registry login (ECR) when there is one. Trivy
# reads it from the environment, so it stays off the command line.
trivy() {
  TRIVY_USERNAME=${ECR:+AWS} TRIVY_PASSWORD=$REG_PASS podman run --rm --network host -e TRIVY_USERNAME -e TRIVY_PASSWORD \
    -v lab02-trivy-cache:/root/.cache "$TRIVY_IMAGE" image --image-src remote --insecure "$@"
}
skopeo() {
  local cmd=$1
  shift
  if [ -n "$ECR" ]; then
    case "$cmd" in
      copy) set -- --src-creds "AWS:$REG_PASS" --dest-creds "AWS:$REG_PASS" "$@" ;;
      *) set -- --creds "AWS:$REG_PASS" "$@" ;;
    esac
  fi
  podman run --rm --network host "$SKOPEO_IMAGE" "$cmd" "$@"
}
