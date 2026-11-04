#!/usr/bin/env bash
# Step 5: ESO synced the Secret, the kubelet updated the file, and the app
# moved to app_v2 in the same pod, with no restart.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
need_cluster
u=$(kubectl -n "$NS" get secret db-app -o jsonpath='{.data.username}' 2>/dev/null | base64 -d)
[ "$u" = app_v2 ] || fail "Secret db-app still holds '$u': ESO syncs every refreshInterval (1h); force a sync"
user=$(api /api/check | jq -r '.user // empty' 2>/dev/null)
[ "$user" = app_v2 ] || fail "the app still answers as '${user:-nothing}': the kubelet updates the file within about a minute"
pod=$(app_pod)
uid=$(kubectl -n "$NS" get pod "$pod" -o jsonpath='{.metadata.uid}')
rec=$(kubectl -n "$NS" get deployment sample-db -o jsonpath='{.metadata.annotations.lab\.devops/pod-uid}')
[ "$uid" = "$rec" ] || fail "the app's pod is not the one from step 1 ($rec): it was restarted or replaced"
rc=$(kubectl -n "$NS" get pod "$pod" -o jsonpath='{.status.containerStatuses[0].restartCount}')
[ "$rc" = 0 ] || fail "the app's container restarted $rc times"
reloads=$(api /metrics | awk '/^sampledb_credential_reloads_total /{print $2}')
[ "${reloads:-0}" != 0 ] || fail "the app reports no credential reload (sampledb_credential_reloads_total)"
ok "the app runs as app_v2 in the same pod $pod, restartCount 0, $reloads reload(s)"
