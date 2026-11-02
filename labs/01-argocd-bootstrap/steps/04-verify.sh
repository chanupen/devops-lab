#!/usr/bin/env bash
# Step 4: podinfo syncs on its own, with self-heal and prune.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
a=$(app podinfo)
jq -e '.spec.syncPolicy.automated' <<<"$a" >/dev/null || fail "podinfo has no automated sync policy (spec.syncPolicy.automated)"
[ "$(jq -r '.spec.syncPolicy.automated.enabled // true' <<<"$a")" = true ] || fail "the automated sync policy is turned off (enabled: false)"
[ "$(jq -r '.spec.syncPolicy.automated.selfHeal // false' <<<"$a")" = true ] || fail "self-heal is off: set spec.syncPolicy.automated.selfHeal: true"
[ "$(jq -r '.spec.syncPolicy.automated.prune // false' <<<"$a")" = true ] || fail "prune is off: set spec.syncPolicy.automated.prune: true"
ok "podinfo syncs on its own, with self-heal and prune"
