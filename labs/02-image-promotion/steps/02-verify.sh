#!/usr/bin/env bash
# Step 2: the registry has lab02-hello:dev, the image built in step 1.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_registry
d=$(tag_digest dev)
[ -n "$d" ] || fail "the registry has no $PUSH/$REPO:dev: podman push --tls-verify=$TLS $REPO:0.1.0 $PUSH/$REPO:dev"
case "$(base_layer dev)" in
  "$VULN_BASE" | "$FIXED_BASE") ok "$PUSH/$REPO:dev is $d" ;;
esac
fail "$PUSH/$REPO:dev is not built from the lab's Dockerfile: push the image from step 1"
