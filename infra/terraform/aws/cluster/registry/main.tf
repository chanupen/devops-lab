locals {
  tags = {
    lab          = "true"
    owner        = var.owner
    "expires-at" = var.expires_at
    component    = "registry"
  }
}

# One repository for every lab image. force_delete lets lab down remove it
# with its images; the lab never keeps anything here it cannot rebuild.
resource "aws_ecr_repository" "lab" {
  name                 = var.repository
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
  repository = aws_ecr_repository.lab.name
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
