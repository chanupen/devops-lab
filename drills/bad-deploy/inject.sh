#!/usr/bin/env bash
# Builds podinfo "6.15.1", a release that exits at start, pushes it to the
# lab registry and deploys it to podinfo-frontend through Argo CD, the way
# a CD tool with Argo CD write-back would. Nothing goes to Git.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
base="${LAB_REGISTRY_PUSH%/lab}/ghcr.io/stefanprodan/podinfo:6.15.0" # through the Zot cache
tag=6.15.1
echo "building podinfo $tag (a bad release) and pushing it to $LAB_REGISTRY_PUSH"
podman build -q --build-arg BASE="$base" -t "$LAB_REGISTRY_PUSH/podinfo:$tag" "$DRILL_DIR/release" >/dev/null
podman push -q --tls-verify=false "$LAB_REGISTRY_PUSH/podinfo:$tag"
echo "deploying it to podinfo-frontend through Argo CD"
release podinfo-frontend "image.repository=$LAB_REGISTRY_PULL/podinfo" "image.tag=$tag"
image() { kubectl -n podinfo get deployment podinfo-frontend -o jsonpath='{.spec.template.spec.containers[0].image}'; }
on_release() { [ "$(image)" = "$LAB_REGISTRY_PULL/podinfo:$tag" ]; }
wait_for 180 "Argo CD to roll out the release" on_release
echo "released"
