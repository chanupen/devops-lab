output "registry" {
  description = "The ECR registry host, <account>.dkr.ecr.<region>.amazonaws.com."
  value       = split("/", values(aws_ecr_repository.lab)[0].repository_url)[0]
}

output "lab_registry" {
  description = "The lab registry, <registry>/<prefix>: LAB_REGISTRY_PUSH and LAB_REGISTRY_PULL in cloud mode."
  value       = "${split("/", values(aws_ecr_repository.lab)[0].repository_url)[0]}/${var.prefix}"
}

output "repository_urls" {
  value = [for r in aws_ecr_repository.lab : r.repository_url]
}
