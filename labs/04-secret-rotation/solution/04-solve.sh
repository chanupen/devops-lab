#!/usr/bin/env bash
# Step 4: store app_v2's credentials as the secret's new current version.
set -euo pipefail
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
set -e
work="${LAB_WORK:-$HOME/lab04}"
aws secretsmanager put-secret-value --secret-id "$APP_SECRET" \
  --secret-string "$(jq -cn --arg p "$(cat "$work/app_v2.password")" '{username: "app_v2", password: $p}')"
