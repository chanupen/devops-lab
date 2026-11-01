# AWS account baseline for the lab: Terraform state bucket, the lab-admin
# role, the monthly budget with its stop action, and the TTL reaper.
# Applied with OpenTofu by `lab cloud baseline plan|apply`, never by hand.
terraform {
  # 1.10 is the first release with native S3 state locking (use_lockfile).
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

  # Credentials come from the environment: lab resolves the profile itself
  # (prompting for the MFA code) and passes temporary keys to tofu.
  default_tags {
    tags = local.tags
  }
}
