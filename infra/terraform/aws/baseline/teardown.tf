# Teardown build: the reaper starts it when a tofu-managed lab resource is
# past expires-at. It runs tofu destroy on the cluster and registry state,
# sweeps with the reaper, and emails a summary through SNS. See teardown.sh.
locals {
  teardown_name = "lab-teardown"
  tofu_version  = "1.13.2"
  tofu_sha256   = "6e815c58a09ee957de606ff48cefad082942e70afc43b737ff975654c54ec453" # tofu_1.13.2_linux_arm64.zip
}

resource "aws_sns_topic" "teardown" {
  name = local.teardown_name
}

# AWS sends a confirmation email first; summaries arrive once it is confirmed.
resource "aws_sns_topic_subscription" "teardown_email" {
  topic_arn = aws_sns_topic.teardown.arn
  protocol  = "email"
  endpoint  = var.alert_email
}

resource "aws_s3_object" "teardown_script" {
  bucket = aws_s3_bucket.state.id
  key    = "teardown/teardown.sh"
  source = "${path.module}/teardown.sh"
  etag   = filemd5("${path.module}/teardown.sh")
}

resource "aws_cloudwatch_log_group" "teardown" {
  name              = "/aws/codebuild/${local.teardown_name}"
  retention_in_days = 14
}

data "aws_iam_policy_document" "codebuild_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["codebuild.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [local.account]
    }
  }
}

resource "aws_iam_role" "teardown" {
  name               = local.teardown_name
  description        = "Runs tofu destroy on an expired cloud lab"
  assume_role_policy = data.aws_iam_policy_document.codebuild_trust.json
}

# tofu destroy of the cluster and registry modules touches EC2/VPC, EKS,
# ELB, SQS, EventBridge and ECR, so those services are open. IAM is not:
# the build may only change roles, policies and instance profiles that
# carry a lab component tag from those modules or from Karpenter, never the
# baseline's (lab-admin, the reaper, this role).
data "aws_iam_policy_document" "teardown" {
  statement {
    sid = "DestroyLabInfrastructure"
    actions = [
      "ec2:*", "eks:*", "elasticloadbalancing:*", "sqs:*", "events:*", "ecr:*",
      "tag:GetResources", "sts:GetCallerIdentity",
    ]
    resources = ["*"]
  }

  statement {
    sid       = "ReadIAM"
    actions   = ["iam:Get*", "iam:List*"]
    resources = ["*"]
  }

  statement {
    sid = "DeleteLabIAM"
    actions = [
      "iam:DeleteRole", "iam:DeleteRolePolicy", "iam:DetachRolePolicy", "iam:DeletePolicy", "iam:DeletePolicyVersion",
      "iam:DeleteInstanceProfile", "iam:RemoveRoleFromInstanceProfile", "iam:UntagRole", "iam:UntagPolicy",
    ]
    resources = ["*"]
    condition {
      test     = "StringEquals"
      variable = "aws:ResourceTag/component"
      values   = ["cluster", "registry", "karpenter"]
    }
  }

  # Karpenter names its instance profiles <cluster>_<hash>.
  statement {
    sid       = "DeleteKarpenterInstanceProfiles"
    actions   = ["iam:RemoveRoleFromInstanceProfile", "iam:DeleteInstanceProfile"]
    resources = ["${local.arn}:iam::${local.account}:instance-profile/lab_*"]
  }

  statement {
    sid       = "StateAndBundles"
    actions   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:ListBucket"]
    resources = [aws_s3_bucket.state.arn, "${aws_s3_bucket.state.arn}/cluster/*", "${aws_s3_bucket.state.arn}/teardown/*"]
  }

  statement {
    sid       = "SweepAndReport"
    actions   = ["lambda:InvokeFunction", "sns:Publish"]
    resources = [aws_lambda_function.reaper.arn, aws_sns_topic.teardown.arn]
  }

  statement {
    sid       = "Logs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.teardown.arn}:*"]
  }
}

resource "aws_iam_role_policy" "teardown" {
  name   = "destroy-expired-lab"
  role   = aws_iam_role.teardown.id
  policy = data.aws_iam_policy_document.teardown.json
}

# arm1.small: ARM_CONTAINER with BUILD_GENERAL1_SMALL, $0.002 a build minute.
resource "aws_codebuild_project" "teardown" {
  name          = local.teardown_name
  description   = "Destroys an expired cloud lab (tofu destroy, reaper sweeps, email summary)"
  service_role  = aws_iam_role.teardown.arn
  build_timeout = 90

  artifacts {
    type = "NO_ARTIFACTS"
  }

  environment {
    type         = "ARM_CONTAINER"
    compute_type = "BUILD_GENERAL1_SMALL"
    image        = "aws/codebuild/amazonlinux2-aarch64-standard:3.0"

    environment_variable {
      name  = "BUCKET"
      value = aws_s3_bucket.state.id
    }
    environment_variable {
      name  = "REAPER"
      value = aws_lambda_function.reaper.function_name
    }
    environment_variable {
      name  = "TOPIC"
      value = aws_sns_topic.teardown.arn
    }
    environment_variable {
      name  = "TOFU_VERSION"
      value = local.tofu_version
    }
    environment_variable {
      name  = "TOFU_SHA256"
      value = local.tofu_sha256
    }
  }

  source {
    type      = "NO_SOURCE"
    buildspec = <<-YAML
      version: 0.2
      phases:
        build:
          commands:
            - aws s3 cp s3://${aws_s3_bucket.state.id}/${aws_s3_object.teardown_script.key} teardown.sh --only-show-errors
            - bash teardown.sh
    YAML
  }

  logs_config {
    cloudwatch_logs {
      group_name = aws_cloudwatch_log_group.teardown.name
    }
  }

  depends_on = [aws_iam_role_policy.teardown]
}
