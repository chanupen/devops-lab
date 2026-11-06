#!/usr/bin/env bash
# Step 3: lab02-hello:dev is rebuilt on the patched base, alpine 3.24.3,
# which Trivy finds no HIGH or CRITICAL CVEs in.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_registry
d=$(tag_digest dev)
[ -n "$d" ] || fail "the registry has no $PUSH/$REPO:dev: push it first (step 2)"
case "$(base_layer dev)" in
  "$FIXED_BASE") ;;
  "$VULN_BASE") fail "$PUSH/$REPO:dev is still on alpine 3.18.0: change FROM to the patched base, rebuild as 0.2.0 and push it as dev" ;;
  *) fail "$PUSH/$REPO:dev is on a base this lab does not know: use alpine 3.24.3 by the digest in the step" ;;
esac
ok "$PUSH/$REPO:dev ($d) is built on alpine 3.24.3"
