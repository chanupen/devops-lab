#!/usr/bin/env bash
# lab teardown build: destroys an expired cloud lab without the laptop.
# The reaper Lambda starts it (CodeBuild project lab-teardown) once a
# tofu-managed resource is past expires-at. It:
#   1. sweeps (the reaper in sweep-only mode: Karpenter nodes, load
#      balancers, target groups, their security groups, volumes);
#   2. destroys the system node group first, which stops Karpenter and the
#      Load Balancer Controller, so nothing new appears behind it;
#   3. sweeps again and destroys the rest of the cluster module, then the
#      registry module (one retry after a sweep if a destroy fails);
#   4. sweeps a last time and emails what is still tagged lab=true.
# A failed run emails a failure summary (what failed, attempt N of M). The
# reaper starts the build again within 15 minutes while lab resources
# remain, at most MAX_ATTEMPTS times, then emails "manual action needed".
# It destroys nothing, and does not sweep, when a newer lab owns a stack:
# the bundle's expires-at is in the future (lab up uploads the bundle
# before every apply, lab extend rewrites it), or a lab apply took the
# stack's state lock less than an hour ago.
# Modules come from s3://$BUCKET/teardown/<stack>.zip, which lab uploads
# before every apply: the exact files and variables it applied.
# Environment: BUCKET, REAPER, TOPIC, AWS_REGION, TOFU_VERSION, TOFU_SHA256,
# ATTEMPT ("unknown" when the reaper could not read its count) and
# MAX_ATTEMPTS (set by the reaper on each start).
set -uo pipefail
export TF_IN_AUTOMATION=1 TF_INPUT=0

attempt=${ATTEMPT:-1} max_attempts=${MAX_ATTEMPTS:-6}
tmp=${TMPDIR:-/tmp} tofu_bin=${TOFU_BIN_DIR:-/usr/local/bin}
lock_stale=3600 # an apply lock older than this is from a lab that died mid-apply
summary="$PWD/summary.txt"
: >"$summary"
result=succeeded
failures=()
note() { echo "$*" | tee -a "$summary"; }
failed() {
  failures+=("$*")
  note "FAILED: $*"
  result=failed
}

# shellcheck disable=SC2329 # run by the EXIT trap
publish() {
  local left subject
  left=$(aws resourcegroupstaggingapi get-resources --tag-filters Key=lab,Values=true --output json |
    jq -r '.ResourceTagMappingList[] | select(([.Tags[] | select(.Key=="component") | .Value] | first) != "baseline") | .ResourceARN' 2>&1)
  [ -n "$left" ] && [ "$result" = succeeded ] && result="finished with leftovers"
  {
    echo
    if [ "$result" = failed ]; then
      echo "FAILURE SUMMARY (attempt $attempt of $max_attempts)"
      for f in "${failures[@]}"; do echo "  - $f"; done
      if [ "$attempt" = unknown ]; then
        echo "The reaper could not read its attempt count, so it does not cap the retries: it starts this build again within 15 minutes while lab resources remain. Check its errors in /aws/lambda/lab-ttl-reaper."
      elif [ "$attempt" -lt "$max_attempts" ]; then
        echo "The reaper starts this build again within 15 minutes while lab resources remain."
      else
        echo "That was the last attempt: the reaper starts no more builds and emails \"manual action needed\"."
      fi
      echo "Log group: /aws/codebuild/lab-teardown"
      echo
    fi
    if [ -z "$left" ]; then
      echo "Nothing tagged lab=true is left outside the baseline."
    else
      echo "Still tagged lab=true outside the baseline (the tagging API can lag a few minutes):"
      while IFS= read -r arn; do echo "  $arn"; done <<<"$left"
    fi
    echo
    echo "Build: ${CODEBUILD_BUILD_ID:-local}"
  } >>"$summary"
  subject="lab teardown $result"
  [ "$result" = failed ] && subject="lab teardown FAILED (attempt $attempt of $max_attempts)"
  aws sns publish --topic-arn "$TOPIC" --subject "$subject" --message "file://$summary" >/dev/null ||
    echo "could not publish the summary"
  cat "$summary"
}
trap publish EXIT

install_tofu() {
  curl -fsSLo "$tmp/tofu.zip" "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/tofu_${TOFU_VERSION}_linux_arm64.zip" &&
    echo "${TOFU_SHA256}  $tmp/tofu.zip" | sha256sum -c - >/dev/null &&
    unzip -oq "$tmp/tofu.zip" tofu -d "$tofu_bin"
}

sweep() {
  if aws lambda invoke --function-name "$REAPER" --cli-binary-format raw-in-base64-out --cli-read-timeout 320 \
    --payload '{"sweepOnly":true}' "$tmp/sweep.json" >"$tmp/invoke.json"; then
    note "sweep ($1): $(jq -c '{deleted: (.Deleted // [] | length), waiting: (.Waiting // []), errors: (.Errors // []), errorMessage}' "$tmp/sweep.json")"
  else
    failed "sweep ($1): could not invoke the reaper"
  fi
}

# fetch <stack>: unpack the stack's bundle into work/<stack>; false if none.
fetch() {
  local dir="work/$1"
  rm -rf "$dir" && mkdir -p "$dir"
  aws s3 cp "s3://$BUCKET/teardown/$1.zip" "$dir/bundle.zip" --only-show-errors 2>/dev/null || return 1
  (cd "$dir" && unzip -q bundle.zip && rm bundle.zip && tofu init -no-color >/dev/null)
}

# blocked <stack>: prints why a newer lab owns the stack, and is false when
# none does. An expires_at it cannot read does not block (fail toward
# teardown); neither does a lock older than lock_stale.
blocked() {
  local dir="work/$1" exp key modified age holder
  exp=$(jq -r '.expires_at // empty' "$dir/lab.auto.tfvars.json" 2>/dev/null)
  if [ -n "$exp" ] && [ "$(date -d "$exp" +%s 2>/dev/null || echo 0)" -gt "$(date +%s)" ]; then
    echo "bundle expires-at $exp is in the future"
    return 0
  fi
  key=$(sed -n 's/^ *key *= *"\(.*\)"/\1/p' "$dir/backend.tf")
  modified=$(aws s3api head-object --bucket "$BUCKET" --key "$key.tflock" --query LastModified --output text 2>/dev/null) || return 1
  age=$(($(date +%s) - $(date -d "$modified" +%s)))
  [ "$age" -lt "$lock_stale" ] || return 1
  holder=$(aws s3 cp "s3://$BUCKET/$key.tflock" - 2>/dev/null | jq -r '"\(.Operation // "an operation") by \(.Who // "someone")"' 2>/dev/null)
  echo "state lock $key.tflock taken ${age}s ago (${holder:-holder unknown}): a lab apply is running"
}

# destroy <stack> [tofu args]: tofu destroy, waiting up to 10 minutes for a
# lock. A lab on a closed laptop can leave a lock behind: one older than
# 30 minutes is removed and the destroy retried.
destroy() {
  local stack=$1; shift
  local dir="work/$stack" lock
  (cd "$dir" && tofu destroy -auto-approve -no-color -lock-timeout=10m "$@") && return 0
  lock=$(sed -n 's/^ *key *= *"\(.*\)"/\1/p' "$dir/backend.tf").tflock
  if aws s3api head-object --bucket "$BUCKET" --key "$lock" --query LastModified --output text 2>/dev/null |
    xargs -r -I{} date -d {} +%s | awk -v now="$(date +%s)" '{exit !(now - $1 > 1800)}'; then
    note "$stack: removing a stale state lock ($lock)"
    aws s3 rm "s3://$BUCKET/$lock" --only-show-errors
  fi
  return 1
}

# Karpenter makes instance profiles for its node role and removes them when
# the EC2NodeClass goes. With Karpenter stopped they stay, and IAM refuses
# to delete a role that is still in a profile, so remove them here.
drop_karpenter_profiles() {
  local role=lab-karpenter-node ip
  for ip in $(aws iam list-instance-profiles-for-role --role-name "$role" \
    --query 'InstanceProfiles[].InstanceProfileName' --output text 2>/dev/null); do
    aws iam remove-role-from-instance-profile --instance-profile-name "$ip" --role-name "$role" &&
      aws iam delete-instance-profile --instance-profile-name "$ip" &&
      note "cluster: removed Karpenter instance profile $ip"
  done
}

# destroy_stack <stack>: the stack's bundle is already in work/<stack>.
destroy_stack() {
  local stack=$1
  if [ "$stack" = cluster ]; then
    if destroy cluster -target=aws_eks_node_group.system; then
      note "cluster: system node group destroyed"
    else
      note "cluster: destroying the node group failed; going on"
    fi
    sweep "after the node group"
    drop_karpenter_profiles
  fi
  if destroy "$stack" || { sweep "before the retry"; destroy "$stack"; }; then
    note "$stack: destroyed"
  else
    failed "$stack: tofu destroy failed twice (see the build log)"
  fi
}

note "lab teardown started $(date -u +%FT%TZ), attempt $attempt of $max_attempts"
install_tofu || { failed "could not install OpenTofu ${TOFU_VERSION}"; exit 1; }
stacks=()
for stack in cluster registry; do
  if fetch "$stack"; then
    stacks+=("$stack")
  else
    note "$stack: no bundle, nothing to destroy"
  fi
done
for stack in "${stacks[@]}"; do
  if why=$(blocked "$stack"); then
    note "$stack: $why"
    note "Not destroying or sweeping anything: a newer lab owns this state. The reaper checks again within 15 minutes."
    result="skipped (a newer lab owns the state)"
    exit 0
  fi
done
sweep "start"
for stack in "${stacks[@]}"; do destroy_stack "$stack"; done
sleep 60 # let load balancer ENIs and instances finish going
sweep "final"
note "lab teardown done $(date -u +%FT%TZ)"
[ "$result" = failed ] && exit 1
exit 0
