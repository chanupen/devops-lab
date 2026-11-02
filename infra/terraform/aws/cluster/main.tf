data "aws_caller_identity" "current" {}
data "aws_partition" "current" {}

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  account = data.aws_caller_identity.current.account_id
  arn     = "arn:${data.aws_partition.current.partition}"
  azs     = slice(sort(data.aws_availability_zones.available.names), 0, 2)

  # Every resource carries these. expires-at lets the TTL reaper delete the
  # lab; lab extend moves it forward on everything at once.
  tags = {
    lab          = "true"
    owner        = var.owner
    "expires-at" = var.expires_at
    component    = "cluster"
  }

  # Karpenter finds the subnets and the security group by this tag.
  discovery = { "karpenter.sh/discovery" = var.cluster_name }
}
