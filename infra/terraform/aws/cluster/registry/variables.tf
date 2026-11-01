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

variable "expires_at" {
  description = "RFC3339 time after which the TTL reaper may delete the lab. lab sets it to now + cloud.ttl; lab extend moves it."
  type        = string

  validation {
    condition     = can(formatdate("YYYY", var.expires_at))
    error_message = "expires_at must be an RFC3339 timestamp."
  }
}

variable "repository" {
  description = "ECR repository for lab images."
  type        = string
  default     = "lab"
}

variable "keep_images" {
  description = "How many images the lifecycle policy keeps; older ones expire."
  type        = number
  default     = 10
}
