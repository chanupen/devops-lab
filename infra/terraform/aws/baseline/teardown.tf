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

# The build reads broadly, but changes only what the lab made:
#   - Describe/List/Get stay open, on every resource, so tofu can refresh
#     the state before it destroys;
#   - every destructive action is held to ap-south-1 (aws:RequestedRegion)
#     and to resources tagged lab=true (aws:ResourceTag/lab), which the
#     cluster and registry modules put on everything (default_tags);
#   - the few deletes whose resource carries no lab tag are named one by
#     one below, each with the narrowest condition IAM offers;
#   - IAM changes only roles, policies and instance profiles that carry a
#     lab component tag, never the baseline's (lab-admin, the reaper, this
#     role). IAM is global, so it has no region condition.
# Load balancers, target groups and the rest of the VPC sweep are left to
# the reaper, which the build invokes.
data "aws_iam_policy_document" "teardown" {
  statement {
    sid = "ReadForRefresh"
    actions = [
      "ec2:Describe*", "ec2:GetLaunchTemplateData",
      "eks:Describe*", "eks:List*",
      "elasticloadbalancing:Describe*",
      "sqs:GetQueueAttributes", "sqs:GetQueueUrl", "sqs:ListQueues", "sqs:ListQueueTags",
      "events:Describe*", "events:List*",
      "ecr:Describe*", "ecr:List*", "ecr:GetLifecyclePolicy", "ecr:GetRepositoryPolicy",
      "tag:GetResources", "sts:GetCallerIdentity",
    ]
    resources = ["*"]
  }

  statement {
    sid = "DestroyLabTaggedInRegion"
    actions = [
      # VPC, subnets, routes, gateway, security groups, launch templates
      "ec2:DeleteVpc", "ec2:DeleteSubnet", "ec2:DeleteRouteTable", "ec2:DeleteRoute", "ec2:DisassociateRouteTable",
      "ec2:DeleteInternetGateway", "ec2:DetachInternetGateway",
      "ec2:DeleteSecurityGroup", "ec2:RevokeSecurityGroupIngress", "ec2:RevokeSecurityGroupEgress",
      "ec2:DeleteLaunchTemplate", "ec2:DeleteLaunchTemplateVersions",
      "ec2:TerminateInstances", "ec2:DeleteVolume", "ec2:DeleteNetworkInterface", "ec2:DeleteTags",
      # EKS
      "eks:DeleteCluster", "eks:DeleteNodegroup", "eks:DeleteAddon", "eks:DeleteAccessEntry",
      "eks:DisassociateAccessPolicy", "eks:DeletePodIdentityAssociation", "eks:UntagResource",
      # Karpenter's interruption queue and rules
      "sqs:DeleteQueue", "sqs:SetQueueAttributes", "sqs:UntagQueue",
      "events:DeleteRule", "events:RemoveTargets", "events:UntagResource",
      # ECR
      "ecr:DeleteRepository", "ecr:DeleteLifecyclePolicy", "ecr:DeleteRepositoryPolicy", "ecr:BatchDeleteImage",
      "ecr:UntagResource",
    ]
    resources = ["*"]
    condition {
      test     = "StringEquals"
      variable = "aws:RequestedRegion"
      values   = [var.region]
    }
    condition {
      test     = "StringEquals"
      variable = "aws:ResourceTag/lab"
      values   = ["true"]
    }
  }

  # The cluster module tags the security group EKS creates for the cluster
  # (karpenter.sh/discovery, aws_ec2_tag); destroying that removes the tag.
  # EKS owns the group, so it has no lab tag: it is matched by the
  # cluster-name tag EKS puts on it, as in the reaper's policy.
  statement {
    sid       = "UntagClusterSecurityGroup"
    actions   = ["ec2:DeleteTags"]
    resources = ["${local.arn}:ec2:${var.region}:${local.account}:security-group/*"]
    condition {
      test     = "StringEquals"
      variable = "aws:ResourceTag/aws:eks:cluster-name"
      values   = ["lab"]
    }
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
    # The reaper overrides these on each start.
    environment_variable {
      name  = "ATTEMPT"
      value = "1"
    }
    environment_variable {
      name  = "MAX_ATTEMPTS"
      value = "6"
    }
  }

  source {
    type      = "NO_SOURCE"
    buildspec = <<-YAML
      version: 0.2
      phases:
        build:
          commands:
            - >-
              aws s3 cp s3://${aws_s3_bucket.state.id}/${aws_s3_object.teardown_script.key} teardown.sh --only-show-errors ||
              { aws sns publish --topic-arn "$TOPIC" --subject "lab teardown FAILED (attempt $${ATTEMPT:-1} of $${MAX_ATTEMPTS:-6})"
              --message "Could not fetch teardown.sh, so nothing was destroyed. Build: $CODEBUILD_BUILD_ID"; exit 1; }
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

# A build that times out, faults or is stopped never reaches teardown.sh's
# summary, so EventBridge emails those endings. Failures inside the script
# are emailed by the script itself, with what failed. Either way the reaper
# starts the build again within 15 minutes while lab resources remain.
resource "aws_cloudwatch_event_rule" "teardown_ended_badly" {
  name        = "${local.teardown_name}-ended-badly"
  description = "Email when the lab teardown build times out, faults or is stopped"
  event_pattern = jsonencode({
    source      = ["aws.codebuild"]
    detail-type = ["CodeBuild Build State Change"]
    detail = {
      project-name = [local.teardown_name]
      build-status = ["TIMED_OUT", "FAULT", "STOPPED"]
    }
  })
}

resource "aws_cloudwatch_event_target" "teardown_ended_badly" {
  rule = aws_cloudwatch_event_rule.teardown_ended_badly.name
  arn  = aws_sns_topic.teardown.arn

  input_transformer {
    input_paths = {
      build  = "$.detail.build-id"
      status = "$.detail.build-status"
    }
    input_template = "\"lab teardown FAILED: build <build> ended <status> before it could send its summary. Lab resources may still bill. The reaper starts the build again within 15 minutes while they remain (at most 6 attempts), then emails 'manual action needed'. Log group: /aws/codebuild/${local.teardown_name}.\""
  }
}

data "aws_iam_policy_document" "teardown_topic" {
  # What the default topic policy allows: this account's own principals.
  statement {
    sid       = "Owner"
    actions   = ["sns:Publish", "sns:Subscribe", "sns:GetTopicAttributes", "sns:SetTopicAttributes", "sns:ListSubscriptionsByTopic"]
    resources = [aws_sns_topic.teardown.arn]
    principals {
      type        = "AWS"
      identifiers = ["${local.arn}:iam::${local.account}:root"]
    }
  }

  statement {
    sid       = "TeardownEndedBadly"
    actions   = ["sns:Publish"]
    resources = [aws_sns_topic.teardown.arn]
    principals {
      type        = "Service"
      identifiers = ["events.amazonaws.com"]
    }
    condition {
      test     = "ArnEquals"
      variable = "aws:SourceArn"
      values   = [aws_cloudwatch_event_rule.teardown_ended_badly.arn]
    }
  }
}

resource "aws_sns_topic_policy" "teardown" {
  arn    = aws_sns_topic.teardown.arn
  policy = data.aws_iam_policy_document.teardown_topic.json
}
