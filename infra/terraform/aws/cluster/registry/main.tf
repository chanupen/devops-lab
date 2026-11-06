locals {
  tags = {
    lab          = "true"
    owner        = var.owner
    "expires-at" = var.expires_at
    component    = "registry"
  }
}

# One repository per lab image, all under the lab/ prefix: the lab registry
# (LAB_REGISTRY_PUSH/PULL) is <account>.dkr.ecr.<region>.amazonaws.com/lab,
# like Zot's writable lab/ namespace in local mode. ECR does not create
# repositories on push, so every image name the lab pushes is listed in
# var.repositories. force_delete lets lab down remove them with their
# images; the lab never keeps anything here it cannot rebuild.
resource "aws_ecr_repository" "lab" {
  for_each             = toset(var.repositories)
  name                 = "${var.prefix}/${each.value}"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "AES256"
  }
}

resource "aws_ecr_lifecycle_policy" "lab" {
  for_each   = aws_ecr_repository.lab
  repository = each.value.name
  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep the last ${var.keep_images} images"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = var.keep_images
      }
      action = { type = "expire" }
    }]
  })
}
