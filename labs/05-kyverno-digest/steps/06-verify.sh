#!/usr/bin/env bash
# Step 6: kyverno test passes offline on the work directory: the policy, a
# tag-only and a digest Pod, and the expected results.
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
dir="${LAB_WORK:-$HOME/lab05}/test"
[ -f "$dir/kyverno-test.yaml" ] || fail "no $dir/kyverno-test.yaml"
out=$(podman run --rm --network none -v "$dir":/test:Z,ro "$KYVERNO_CLI" test /test 2>&1)
rc=$?
summary=$(grep -o 'Test Summary: [0-9]* tests passed and [0-9]* tests failed' <<<"$out")
[ $rc -eq 0 ] && [ -n "$summary" ] || fail "kyverno test fails: ${summary:-$(grep -iE 'error|fail' <<<"$out" | head -1)}"
passed=$(awk '{print $3}' <<<"$summary")
[ "$passed" -ge 2 ] || fail "kyverno test checks only $passed result(s); test a tag-only Pod (fail) and a digest Pod (pass)"
ok "kyverno test passes offline: $summary"
