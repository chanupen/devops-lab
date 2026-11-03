#!/usr/bin/env bash
# Step 1: image lab02-hello:0.1.0 is built with rootless Podman from the
# lab's Dockerfile (alpine 3.18.0 base), runs as a non-root user.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
podman image exists localhost/$REPO:0.1.0 || fail "no image localhost/$REPO:0.1.0 in Podman: podman build -t $REPO:0.1.0 ~/lab02"
base=$(podman image inspect localhost/$REPO:0.1.0 --format '{{index .RootFS.Layers 0}}')
[ "$base" = "$VULN_BASE" ] || fail "localhost/$REPO:0.1.0 is not built on the lab's alpine 3.18.0 base: build it from the Dockerfile as copied, unchanged"
user=$(podman image inspect localhost/$REPO:0.1.0 --format '{{.Config.User}}')
[ "$user" = 10001 ] || fail "the image runs as user '$user', want 10001 from the Dockerfile"
ok "localhost/$REPO:0.1.0 is built on alpine 3.18.0 and runs as user 10001"
