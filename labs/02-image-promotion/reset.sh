#!/usr/bin/env bash
# Back to the start of lab 2: no lab02-* namespaces, no lab02-hello images
# in Podman or in the lab registry. The Trivy cache volume is kept, so the
# next scan does not download the vulnerability database again.
set -uo pipefail
# shellcheck source=SCRIPTDIR/steps/lib.sh
. "$(dirname "$0")/steps/lib.sh"
kubectl delete namespace lab02-dev lab02-prod --ignore-not-found --wait --timeout=180s
# The registry: delete each tag's manifest by digest.
for tag in $(curl -fsS -m 10 "$API/tags/list" 2>/dev/null | jq -r '.tags[]?'); do
  d=$(tag_digest "$tag")
  [ -n "$d" ] && curl -fsS -m 10 -o /dev/null -X DELETE "$API/manifests/$d"
done
podman rmi -f localhost/lab02-hello:0.1.0 localhost/lab02-hello:0.2.0 >/dev/null 2>&1
echo "lab 2 is back at its start; your work directory (~/lab02) is left as it is: step 1 copies the app again"
