#!/usr/bin/env bash
# Step 3: Application podinfo deploys charts/podinfo from the podinfo
# repo at 6.17.0 to namespace podinfo, and is Synced and Healthy.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
a=$(app podinfo) || fail "$a"
repo=$(jq -r '.spec.source.repoURL // ""' <<<"$a" | sed 's#\.git$##; s#/$##')
path=$(jq -r '.spec.source.path // ""' <<<"$a")
rev=$(jq -r '.spec.source.targetRevision // ""' <<<"$a")
ns=$(jq -r '.spec.destination.namespace // ""' <<<"$a")
[ "$repo" = https://github.com/stefanprodan/podinfo ] || fail "the source repo is '$repo', want https://github.com/stefanprodan/podinfo"
[ "$path" = charts/podinfo ] || fail "the source path is '$path', want charts/podinfo"
[ "$rev" = 6.17.0 ] || fail "the target revision is '$rev', want the tag 6.17.0"
[ "$ns" = podinfo ] || fail "the destination namespace is '$ns', want podinfo"
sync=$(jq -r '.status.sync.status // "Unknown"' <<<"$a")
health=$(jq -r '.status.health.status // "Unknown"' <<<"$a")
[ "$sync" = Synced ] || fail "podinfo is $sync, not Synced: argocd app sync podinfo"
[ "$health" = Healthy ] || fail "podinfo is Synced but $health, not Healthy yet: argocd app wait podinfo --health"
ok "podinfo is Synced and Healthy"
