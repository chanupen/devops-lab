output "state_bucket" {
  description = "S3 bucket for Terraform state; lab migrates the baseline's own state into it."
  value       = aws_s3_bucket.state.bucket
}

output "lab_admin_role_arn" {
  description = "Role the lab CLI profile assumes with MFA."
  value       = aws_iam_role.lab_admin.arn
}

output "budget_name" {
  value = aws_budgets_budget.monthly.name
}

output "reaper_function" {
  value = aws_lambda_function.reaper.function_name
}

output "reaper_dry_run" {
  value = var.reaper_dry_run
}
