# lab-admin: what the lab CLI profile assumes. Only the source user can
# assume it, only with MFA, for at most 4 hours.
data "aws_iam_policy_document" "lab_admin_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "AWS"
      identifiers = ["${local.arn}:iam::${local.account}:user/${var.source_user}"]
    }
    condition {
      test     = "Bool"
      variable = "aws:MultiFactorAuthPresent"
      values   = ["true"]
    }
  }
}

resource "aws_iam_role" "lab_admin" {
  name                 = "lab-admin"
  description          = "Assumed by the lab CLI profile with MFA"
  assume_role_policy   = data.aws_iam_policy_document.lab_admin_trust.json
  max_session_duration = 14400
}

resource "aws_iam_role_policy_attachment" "lab_admin" {
  role       = aws_iam_role.lab_admin.name
  policy_arn = "${local.arn}:iam::aws:policy/AdministratorAccess"
}
