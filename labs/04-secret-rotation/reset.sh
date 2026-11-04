#!/usr/bin/env bash
# Back to the start of lab 4: no lab04-* namespaces and no lab04/ secrets
# in Secrets Manager. Your work directory (~/lab04) is left as it is.
set -uo pipefail
# shellcheck source=SCRIPTDIR/steps/lib.sh
. "$(dirname "$0")/steps/lib.sh"
kubectl delete namespace "$NS" "$TNS" --ignore-not-found --wait --timeout=180s
for s in "$APP_SECRET" "$ADMIN_SECRET"; do
  aws secretsmanager delete-secret --secret-id "$s" --force-delete-without-recovery >/dev/null 2>&1
done
echo "lab 4 is back at its start; ~/lab04 is left as it is"
