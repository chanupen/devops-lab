data "aws_caller_identity" "current" {}
data "aws_partition" "current" {}

locals {
  account = data.aws_caller_identity.current.account_id
  arn     = "arn:${data.aws_partition.current.partition}"

  # Baseline resources are permanent: lab and owner, but no expires-at, so
  # the TTL reaper (which deletes only what has an expired expires-at)
  # never touches them.
  tags = {
    lab       = "true"
    owner     = var.owner
    component = "baseline"
  }

  state_bucket = "lab-tofu-state-${local.account}" # must match StateBucket in internal/provider/aws
}
