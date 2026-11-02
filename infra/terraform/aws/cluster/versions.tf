# Cloud cluster for the lab: VPC, EKS, the system node group, the IAM side
# of Karpenter and the AWS Load Balancer Controller, and Pod Identity.
# Run by `lab up --mode cloud` (cluster stack) with OpenTofu, never by hand.
# The registry stack (ECR) is the separate root module in registry/.
terraform {
  required_version = ">= 1.10.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region = var.region

  # Credentials come from the environment: lab resolves the lab profile
  # (MFA once per session) and passes temporary keys to tofu.
  default_tags {
    tags = local.tags
  }
}
