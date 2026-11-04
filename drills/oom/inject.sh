#!/usr/bin/env bash
# Releases a podinfo-backend that keeps a 128 MB catalogue in memory
# (podinfo --stress-memory=128) through Argo CD, the way a CD tool with
# Argo CD write-back would. Its memory limit stays at the 64Mi from Git,
# far below the ~140Mi it now needs. Nothing goes to Git.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
set -e
need_cluster
echo "releasing podinfo-backend with an in-memory catalogue (128 MB) through Argo CD"
release podinfo-backend "extraArgs[0]=--stress-memory=128"
args() { kubectl -n podinfo get deployment podinfo-backend -o jsonpath='{.spec.template.spec.containers[0].command}'; }
released() { [[ "$(args)" == *--stress-memory=128* ]]; }
wait_for 180 "Argo CD to roll out the release" released
echo "released"
