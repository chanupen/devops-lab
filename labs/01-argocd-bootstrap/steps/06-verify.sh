#!/usr/bin/env bash
# Step 6: Application root is Synced and manages two Applications, both
# Synced.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
a=$(app root) || fail "$a"
path=$(jq -r '.spec.source.path // ""' <<<"$a")
[ "$path" = apps ] || fail "root's source path is '$path', want apps"
jq -e '.spec.syncPolicy.automated' <<<"$a" >/dev/null || fail "root has no automated sync policy"
sync=$(jq -r '.status.sync.status // "Unknown"' <<<"$a")
[ "$sync" = Synced ] || fail "root is $sync, not Synced"
children=$(jq -r '[.status.resources[]? | select(.kind == "Application") | .name] | sort | join(" ")' <<<"$a")
count=$(wc -w <<<"$children")
[ "$count" -eq 2 ] || fail "root manages $count Applications (${children:-none}), want 2: guestbook and helm-guestbook in its Helm values"
for c in $children; do
  s=$(kubectl -n argocd get application "$c" -o jsonpath='{.status.sync.status}' 2>/dev/null)
  [ "$s" = Synced ] || fail "child $c is ${s:-not there yet}, not Synced: watch it with argocd app get $c"
done
ok "root manages $children, all Synced"
