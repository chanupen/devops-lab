#!/usr/bin/env bash
# Step 6: send Karpenter the spot interruption warning EC2 would send for
# the node inflate runs on: an EventBridge event, as a message on
# Karpenter's interruption queue. Karpenter cannot tell it from a real one.
set -euo pipefail
# shellcheck source=SCRIPTDIR/lib.sh
. "$(dirname "$0")/lib.sh"
node=$(pool_nodes | head -1)
[ -n "$node" ] || { echo "no node of $POOL to interrupt" >&2; exit 1; }
provider=$(kubectl get node "$node" -o jsonpath='{.spec.providerID}') # aws:///ap-south-1a/i-0123...
instance=${provider##*/}
queue=$(kubectl -n kube-system get deployment karpenter \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="INTERRUPTION_QUEUE")].value}')
account=$(aws sts get-caller-identity --query Account --output text)
url=$(aws sqs get-queue-url --queue-name "$queue" --query QueueUrl --output text)
body=$(jq -cn --arg id "$instance" --arg acct "$account" --arg region "${AWS_REGION:?lab labs env sets it}" \
  --arg time "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg uuid "$(cat /proc/sys/kernel/random/uuid)" '{
  version: "0", id: $uuid, "detail-type": "EC2 Spot Instance Interruption Warning",
  source: "aws.ec2", account: $acct, time: $time, region: $region,
  resources: ["arn:aws:ec2:\($region):\($acct):instance/\($id)"],
  detail: {"instance-id": $id, "instance-action": "terminate"}}')
echo "interrupting $node ($instance) through $queue"
aws sqs send-message --queue-url "$url" --message-body "$body" >/dev/null
wait_step 6 8
