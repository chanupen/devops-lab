# TTL reaper: every 15 minutes, delete lab=true resources whose expires-at
# has passed. In dry-run mode it only logs, and its role cannot delete.
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
    sid       = "FindTaggedResources"
    actions   = ["tag:GetResources", "ec2:DescribeInstances", "ec2:DescribeVolumes", "ec2:DescribeAddresses", "eks:DescribeNodegroup", "eks:DescribeCluster", "eks:ListNodegroups"]
    resources = ["*"]
  }

  # Deletes exist only when dry-run is off, and only for lab=true resources.
  dynamic "statement" {
    for_each = var.reaper_dry_run ? [] : [1]
    content {
      sid = "DeleteExpiredLabResources"
      actions = [
        "ec2:TerminateInstances",
        "ec2:DeleteVolume",
        "ec2:ReleaseAddress",
        "eks:DeleteNodegroup",
        "eks:DeleteCluster",
        "elasticloadbalancing:DeleteLoadBalancer",
      ]
      resources = ["*"]
      condition {
        test     = "StringEquals"
        variable = "aws:ResourceTag/lab"
        values   = ["true"]
      }
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
  timeout          = 120
  memory_size      = 128

  environment {
    variables = {
      DRY_RUN = tostring(var.reaper_dry_run)
    }
  }

  depends_on = [aws_cloudwatch_log_group.reaper, aws_iam_role_policy.reaper]
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
