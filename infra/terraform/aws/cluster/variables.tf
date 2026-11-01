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

variable "cluster_name" {
  description = "EKS cluster name."
  type        = string
  default     = "lab"
}

variable "k8s_version" {
  description = "Kubernetes minor version; the lab is pinned to one (lab.yaml k8sVersion)."
  type        = string
  default     = "1.37"
}

variable "api_allowed_cidrs" {
  description = "CIDRs that may reach the public EKS API endpoint. lab passes the current public IP as a /32."
  type        = list(string)

  validation {
    condition     = length(var.api_allowed_cidrs) > 0 && alltrue([for c in var.api_allowed_cidrs : c != "0.0.0.0/0"])
    error_message = "The API endpoint must be restricted; 0.0.0.0/0 is not allowed."
  }
}

variable "vpc_cidr" {
  description = "VPC CIDR; the two public subnets are its first two /20s."
  type        = string
  default     = "10.40.0.0/16"
}

variable "system_instance_type" {
  description = "Instance type of the on-demand system node group (arm64)."
  type        = string
  default     = "t4g.medium"
}
