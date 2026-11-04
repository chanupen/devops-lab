#!/usr/bin/env bash
# lab has put lab-bootstrap and podinfo-frontend back (auto-sync on, no
# parameters), so Argo CD syncs Git's release again. Wait for it. The bad
# image stays in the lab registry, where it harms nothing.
# shellcheck source=SCRIPTDIR/../lib.sh
. "$(dirname "$0")/../lib.sh"
need_cluster
wait_lab_back podinfo-frontend
wait_for 180 "podinfo-frontend to roll out" rolled_out podinfo podinfo-frontend
echo "podinfo-frontend is back on the release in Git"
