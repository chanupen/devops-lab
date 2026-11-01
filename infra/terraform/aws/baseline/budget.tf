# Monthly cost budget with alerts at 50, 80 and 100 percent of actual spend.
# At 100 percent, an action attaches a deny on ec2:RunInstances to lab-admin,
# so nothing new can start until the policy is detached (see docs/design.md).
resource "aws_budgets_budget" "monthly" {
  name         = "lab-monthly"
  budget_type  = "COST"
  limit_amount = format("%.2f", var.budget_usd)
  limit_unit   = "USD"
  time_unit    = "MONTHLY"

  dynamic "notification" {
    for_each = [50, 80, 100]
    content {
      comparison_operator        = "GREATER_THAN"
      threshold                  = notification.value
      threshold_type             = "PERCENTAGE"
      notification_type          = "ACTUAL"
      subscriber_email_addresses = [var.alert_email]
    }
  }
}

data "aws_iam_policy_document" "deny_run_instances" {
  statement {
    sid       = "BudgetExhaustedNoNewInstances"
    effect    = "Deny"
    actions   = ["ec2:RunInstances"]
    resources = ["*"]
  }
}

resource "aws_iam_policy" "deny_run_instances" {
  name        = "lab-budget-deny-run-instances"
  description = "Attached to lab-admin by the budget action at 100% of the monthly budget"
  policy      = data.aws_iam_policy_document.deny_run_instances.json
}

# The role AWS Budgets uses to run the action: it may attach and detach
# only that one policy, and only on lab-admin.
data "aws_iam_policy_document" "budgets_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["budgets.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [local.account]
    }
  }
}

data "aws_iam_policy_document" "budgets_action" {
  statement {
    actions   = ["iam:AttachRolePolicy", "iam:DetachRolePolicy"]
    resources = [aws_iam_role.lab_admin.arn]
    condition {
      test     = "ArnEquals"
      variable = "iam:PolicyARN"
      values   = [aws_iam_policy.deny_run_instances.arn]
    }
  }
}

resource "aws_iam_role" "budgets_action" {
  name               = "lab-budgets-action"
  description        = "Lets AWS Budgets attach the deny policy to lab-admin"
  assume_role_policy = data.aws_iam_policy_document.budgets_trust.json
}

resource "aws_iam_role_policy" "budgets_action" {
  name   = "attach-deny-to-lab-admin"
  role   = aws_iam_role.budgets_action.id
  policy = data.aws_iam_policy_document.budgets_action.json
}

resource "aws_budgets_budget_action" "stop_at_100" {
  budget_name        = aws_budgets_budget.monthly.name
  action_type        = "APPLY_IAM_POLICY"
  approval_model     = "AUTOMATIC"
  notification_type  = "ACTUAL"
  execution_role_arn = aws_iam_role.budgets_action.arn

  action_threshold {
    action_threshold_type  = "PERCENTAGE"
    action_threshold_value = 100
  }

  definition {
    iam_action_definition {
      policy_arn = aws_iam_policy.deny_run_instances.arn
      roles      = [aws_iam_role.lab_admin.name]
    }
  }

  subscriber {
    address           = var.alert_email
    subscription_type = "EMAIL"
  }

  depends_on = [aws_iam_role_policy.budgets_action]
}
