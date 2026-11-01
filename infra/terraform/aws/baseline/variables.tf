variable "region" {
  description = "The only region the lab uses."
  type        = string
  default     = "ap-south-1"

  validation {
    condition     = var.region == "ap-south-1"
    error_message = "The lab runs only in ap-south-1."
  }
}

variable "owner" {
  description = "Value of the owner tag on every resource."
  type        = string
}

variable "source_user" {
  description = "IAM user allowed to assume lab-admin (with MFA)."
  type        = string
  default     = "devops"
}

variable "alert_email" {
  description = "Where AWS Budgets sends the 50/80/100% alerts. Kept out of the repo."
  type        = string
  sensitive   = true
}

variable "budget_usd" {
  description = "Monthly cost budget in USD."
  type        = number
  default     = 30
}

variable "reaper_zip" {
  description = "Path to the reaper Lambda package (make reaper builds bin/reaper.zip)."
  type        = string
}

variable "reaper_dry_run" {
  description = "When true the reaper only logs what it would delete, and its role has no delete permissions."
  type        = bool
  default     = true
}
