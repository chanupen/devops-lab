# ECR for the lab's images: the registry stack in cloud mode.
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
