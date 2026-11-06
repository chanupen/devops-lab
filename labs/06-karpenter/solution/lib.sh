# shellcheck shell=bash
# APP is used by the scripts that source this file.
# shellcheck disable=SC2034
# Shared by lab 6's solutions: the verify library, and a wait on a step's
# own verify script (Karpenter takes a minute or two to launch or remove a
# node).
# shellcheck source=SCRIPTDIR/../steps/lib.sh
. "$(dirname "$0")/../steps/lib.sh"
set -e
APP="$(dirname "$0")/../app"

# wait_step <n> <minutes>: until step n's verify passes.
wait_step() {
  local out deadline=$((SECONDS + $2 * 60))
  while :; do
    if out=$("$(dirname "$0")/../steps/0$1-verify.sh" 2>&1); then
      echo "$out"
      return 0
    fi
    [ "$SECONDS" -lt "$deadline" ] || { echo "step $1 did not pass within $2 minutes: $(tail -1 <<<"$out")" >&2; return 1; }
    sleep 5
  done
}
