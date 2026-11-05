# TTL reaper: every 15 minutes, delete controller-created lab=true resources
# whose expires-at has passed, and everything inside an expired lab VPC,
# and start the teardown build (teardown.tf) for the tofu-managed rest:
# again while they remain, at most 6 builds, then it emails "manual action
# needed". Each scheduled run tests itself (it can read its teardown state
# and see the teardown project) and writes a heartbeat to the state bucket;
# lab up --mode cloud refuses unless the latest one is recent and clean.
# A run that returns an error emails the owner (on-failure destination,
# SNS lab-teardown). In dry-run mode it only logs, and its role cannot
# delete or start builds.
locals {
  reaper_name = "lab-ttl-reaper"
}

resource "aws_cloudwatch_log_group" "reaper" {
  name              = "/aws/lambda/${local.reaper_name}"
  retention_in_days = 14
}

data "aws_iam_policy_document" "lambda_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "reaper" {
  name               = local.reaper_name
  assume_role_policy = data.aws_iam_policy_document.lambda_trust.json
}

data "aws_iam_policy_document" "reaper" {
  statement {
    sid       = "Logs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.reaper.arn}:*"]
  }

  statement {
    sid = "FindTaggedResources"
    actions = [
      "tag:GetResources", "ec2:DescribeInstances", "ec2:DescribeVolumes", "ec2:DescribeAddresses",
      "ec2:DescribeSecurityGroupRules", "ec2:DescribeSecurityGroups", "ec2:DescribeNetworkInterfaces",
      "elasticloadbalancing:DescribeLoadBalancers", "elasticloadbalancing:DescribeTargetGroups",
      "eks:DescribeNodegroup", "eks:DescribeCluster", "eks:ListNodegroups",
      # Confirm before acting: the tagging API lags deletions by up to an hour.
      "ec2:DescribeVpcs", "ec2:DescribeSubnets", "ec2:DescribeRouteTables", "ec2:DescribeInternetGateways",
      "ec2:DescribeLaunchTemplates", "ecr:DescribeRepositories", "secretsmanager:DescribeSecret",
      "codebuild:ListBuildsForProject", "codebuild:BatchGetBuilds",
    ]
    resources = ["*"]
  }

  statement {
    sid       = "Heartbeat"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.state.arn}/reaper/heartbeat.json"]
  }

  # How many teardown builds this lab has had (teardown/state.json).
  # Without s3:ListBucket, S3 answers a GetObject on a missing key with 403,
  # not 404, and the reaper cannot tell "no state yet" from "no access".
  # On 2 Nov 2026 that 403 stopped every teardown for five hours. Listing
  # is held to the teardown/ prefix and the two stack state keys; if S3
  # still answers 403 for a missing key, the reaper lists the key itself
  # (internal/reaper getObject).
  statement {
    sid       = "ListTeardownAndStateKeys"
    actions   = ["s3:ListBucket"]
    resources = [aws_s3_bucket.state.arn]
    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = ["teardown/", "teardown/*", "cluster/terraform.tfstate", "cluster/registry/terraform.tfstate"]
    }
  }

  # The stack gate: a teardown build starts only for a stack whose state
  # holds resources (and whose bundle under teardown/ has expired). Read
  # only: the states hold no secrets the reaper could not describe anyway,
  # but it never writes them.
  statement {
    sid     = "ReadStackStates"
    actions = ["s3:GetObject"]
    resources = [
      "${aws_s3_bucket.state.arn}/cluster/terraform.tfstate",
      "${aws_s3_bucket.state.arn}/cluster/registry/terraform.tfstate",
    ]
  }

  statement {
    sid       = "ReadTeardownPrefix"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.state.arn}/teardown/*"]
  }

  # Writes only its own state: teardown/ also holds teardown.sh and the
  # module bundles, which the teardown build runs with destroy rights.
  statement {
    sid       = "TeardownAttempts"
    actions   = ["s3:PutObject", "s3:DeleteObject"]
    resources = ["${aws_s3_bucket.state.arn}/teardown/state.json"]
  }

  # The "manual action needed" email after the last attempt, and the
  # on-failure destination below, which publishes with this role.
  statement {
    sid       = "EmailOwner"
    actions   = ["sns:Publish"]
    resources = [aws_sns_topic.teardown.arn]
  }

  # The self-test: the teardown project exists and the reaper can see it.
  statement {
    sid       = "SelfTestTeardownProject"
    actions   = ["codebuild:BatchGetProjects"]
    resources = ["${local.arn}:codebuild:${var.region}:${local.account}:project/${local.teardown_name}"]
  }

  # Deletes exist only when dry-run is off, only in the lab region, and only
  # for lab=true resources.
  dynamic "statement" {
    for_each = var.reaper_dry_run ? [] : [1]
    content {
      sid = "DeleteExpiredLabResources"
      actions = [
        "ec2:TerminateInstances",
        "ec2:DeleteVolume",
        "ec2:ReleaseAddress",
        "ec2:DeleteSecurityGroup",
        "ec2:DeleteNetworkInterface",
        "eks:DeleteNodegroup",
        "eks:DeleteCluster",
        "elasticloadbalancing:DeleteLoadBalancer",
        "elasticloadbalancing:DeleteTargetGroup",
        # The sample-db stack's credentials, deleted without a recovery
        # window (ForceDeleteWithoutRecovery), so nothing stays billed.
        "secretsmanager:DeleteSecret",
      ]
      resources = ["*"]
      condition {
        test     = "StringEquals"
        variable = "aws:ResourceTag/lab"
        values   = ["true"]
      }
      condition {
        test     = "StringEquals"
        variable = "aws:RequestedRegion"
        values   = [var.region]
      }
    }
  }

  # The VPC sweep: what sits inside an expired lab VPC is deleted tagged or
  # not (an untagged Classic Load Balancer from a plain type: LoadBalancer
  # Service, its ENIs and security group would block the VPC destroy).
  # IAM cannot name "the lab VPC" in advance: ELB has no VPC condition key,
  # and the VPC ID exists only once the cluster module ran. So this is held
  # to the lab region and to the reaper's code, which only sweeps VPCs that
  # are tagged lab=true and past expires-at (internal/reaper InVPC).
  dynamic "statement" {
    for_each = var.reaper_dry_run ? [] : [1]
    content {
      sid = "SweepExpiredLabVPC"
      actions = [
        "ec2:DeleteSecurityGroup",
        "ec2:DeleteNetworkInterface",
        "ec2:RevokeSecurityGroupIngress",
        "elasticloadbalancing:DeleteLoadBalancer",
        "elasticloadbalancing:DeleteTargetGroup",
      ]
      resources = ["*"]
      condition {
        test     = "StringEquals"
        variable = "aws:RequestedRegion"
        values   = [var.region]
      }
    }
  }

  # Before deleting a controller's security group, the rule pointing at it
  # in the EKS cluster security group goes. That group is created by EKS,
  # so it is matched by the cluster-name tag EKS puts on it.
  dynamic "statement" {
    for_each = var.reaper_dry_run ? [] : [1]
    content {
      sid       = "RevokeRulesOnLabGroups"
      actions   = ["ec2:RevokeSecurityGroupIngress"]
      resources = ["*"]
      condition {
        test     = "StringEquals"
        variable = "aws:ResourceTag/aws:eks:cluster-name"
        values   = ["lab"]
      }
    }
  }

  dynamic "statement" {
    for_each = var.reaper_dry_run ? [] : [1]
    content {
      sid       = "StartTeardown"
      actions   = ["codebuild:StartBuild"]
      resources = ["${local.arn}:codebuild:${var.region}:${local.account}:project/${local.teardown_name}"] # by name: the project depends on the reaper
    }
  }
}

resource "aws_iam_role_policy" "reaper" {
  name   = "reap-expired-lab-resources"
  role   = aws_iam_role.reaper.id
  policy = data.aws_iam_policy_document.reaper.json
}

resource "aws_lambda_function" "reaper" {
  function_name    = local.reaper_name
  description      = "Deletes lab=true resources past expires-at (dry run: logs only)"
  role             = aws_iam_role.reaper.arn
  filename         = var.reaper_zip
  source_code_hash = filebase64sha256(var.reaper_zip)
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  timeout          = 300
  memory_size      = 128

  environment {
    variables = {
      DRY_RUN          = tostring(var.reaper_dry_run)
      TEARDOWN_PROJECT = local.teardown_name
      TEARDOWN_TOPIC   = aws_sns_topic.teardown.arn
      HEARTBEAT_BUCKET = aws_s3_bucket.state.id
    }
  }

  depends_on = [aws_cloudwatch_log_group.reaper, aws_iam_role_policy.reaper]
}

# Any scheduled run that returns an error (EventBridge invokes the Lambda
# asynchronously) emails the owner once Lambda's retries are spent, about
# 3 minutes later. The teardown build's sweep-only calls are synchronous
# and do not go through this; the build reports its own failures.
resource "aws_lambda_function_event_invoke_config" "reaper" {
  function_name          = aws_lambda_function.reaper.function_name
  maximum_retry_attempts = 2

  destination_config {
    on_failure {
      destination = aws_sns_topic.teardown.arn
    }
  }
}

resource "aws_cloudwatch_event_rule" "reaper" {
  name                = local.reaper_name
  description         = "Run the lab TTL reaper every 15 minutes"
  schedule_expression = "rate(15 minutes)"
}

resource "aws_cloudwatch_event_target" "reaper" {
  rule = aws_cloudwatch_event_rule.reaper.name
  arn  = aws_lambda_function.reaper.arn
}

resource "aws_lambda_permission" "reaper" {
  statement_id  = "AllowEventBridge"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.reaper.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.reaper.arn
}
