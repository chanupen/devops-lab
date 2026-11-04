#!/usr/bin/env bash
# lab has put lab-bootstrap and podinfo-backend back (no parameters), so
# Argo CD syncs Git's release again. Wait for it.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
wait_lab_back podinfo-backend
wait_for 180 "podinfo-backend to roll out" rolled_out podinfo podinfo-backend
echo "podinfo-backend is back on the release in Git"
