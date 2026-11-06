# EKS control plane. The public endpoint answers only the CIDRs lab passes
# (the current public IP); nodes use the private endpoint inside the VPC.
data "aws_iam_policy_document" "eks_trust" {
  statement {
    actions = ["sts:AssumeRole", "sts:TagSession"]
    principals {
      type        = "Service"
      identifiers = ["eks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "cluster" {
  name               = "${var.cluster_name}-eks-cluster"
  assume_role_policy = data.aws_iam_policy_document.eks_trust.json
}

resource "aws_iam_role_policy_attachment" "cluster" {
  role       = aws_iam_role.cluster.name
  policy_arn = "${local.arn}:iam::aws:policy/AmazonEKSClusterPolicy"
}

resource "aws_eks_cluster" "lab" {
  name     = var.cluster_name
  version  = var.k8s_version
  role_arn = aws_iam_role.cluster.arn

  # No control plane log types: CloudWatch costs more than the lab needs.
  enabled_cluster_log_types = []

  access_config {
    authentication_mode                         = "API"
    bootstrap_cluster_creator_admin_permissions = true
  }

  vpc_config {
    subnet_ids              = aws_subnet.public[*].id
    endpoint_private_access = true
    endpoint_public_access  = true
    public_access_cidrs     = var.api_allowed_cidrs
  }

  # Standard support only: extended support costs six times as much.
  upgrade_policy {
    support_type = "STANDARD"
  }

  depends_on = [aws_iam_role_policy_attachment.cluster]
}

# Node role shared by the system node group; Karpenter's nodes get their
# own role from the karpenter module.
data "aws_iam_policy_document" "ec2_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "node" {
  name               = "${var.cluster_name}-system-node"
  assume_role_policy = data.aws_iam_policy_document.ec2_trust.json
}

resource "aws_iam_role_policy_attachment" "node" {
  for_each = toset([
    "AmazonEKSWorkerNodePolicy",
    "AmazonEKS_CNI_Policy",
    "AmazonEC2ContainerRegistryPullOnly",
  ])
  role       = aws_iam_role.node.name
  policy_arn = "${local.arn}:iam::aws:policy/${each.value}"
}

# The launch template exists to tag the instance, its volume and its network
# interface (node group tags do not reach them) and to require IMDSv2.
# It sets no security groups, so EKS attaches the cluster security group.
resource "aws_launch_template" "system" {
  name_prefix = "${var.cluster_name}-system-"

  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 2
  }

  block_device_mappings {
    device_name = "/dev/xvda"
    ebs {
      volume_size           = 20
      volume_type           = "gp3"
      encrypted             = true
      delete_on_termination = true
    }
  }

  dynamic "tag_specifications" {
    for_each = ["instance", "volume", "network-interface"]
    content {
      resource_type = tag_specifications.value
      tags          = merge(local.tags, { Name = "${var.cluster_name}-system" })
    }
  }

  # lab extend retags running instances itself. Following expires-at here
  # would make a new template version and roll the node on every extend.
  lifecycle {
    ignore_changes = [tag_specifications]
  }
}

# One small on-demand arm64 node for system pods: CoreDNS, Karpenter and the
# Load Balancer Controller. It is tainted, so workloads go to Karpenter's
# spot nodes.
resource "aws_eks_node_group" "system" {
  cluster_name    = aws_eks_cluster.lab.name
  node_group_name = "system"
  node_role_arn   = aws_iam_role.node.arn
  subnet_ids      = aws_subnet.public[*].id
  ami_type        = "AL2023_ARM_64_STANDARD"
  capacity_type   = "ON_DEMAND"
  instance_types  = [var.system_instance_type]

  launch_template {
    id      = aws_launch_template.system.id
    version = aws_launch_template.system.latest_version
  }

  scaling_config {
    desired_size = 1
    min_size     = 1
    max_size     = 1
  }

  update_config {
    max_unavailable = 1
  }

  labels = { "lab.dev/role" = "system" }

  taint {
    key    = "CriticalAddonsOnly"
    value  = "true"
    effect = "NO_SCHEDULE"
  }

  # The VPC CNI's prefix delegation must be on before the node joins, so
  # its max pods follow it.
  depends_on = [aws_iam_role_policy_attachment.node, aws_eks_addon.before_nodes]
}

# Managed add-ons at the default version for the cluster's Kubernetes
# version. The Pod Identity agent lets pods assume IAM roles without IRSA.
locals {
  addons_before_nodes = ["vpc-cni", "kube-proxy", "eks-pod-identity-agent"]
}

resource "aws_eks_addon" "before_nodes" {
  for_each                    = toset(local.addons_before_nodes)
  cluster_name                = aws_eks_cluster.lab.name
  addon_name                  = each.value
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "OVERWRITE"

  # Prefix delegation: each ENI slot holds a /28, so small nodes are no
  # longer capped at 11 (t4g.small) or 17 (t4g.medium) pods. The managed
  # node group sets max pods from it; Karpenter's EC2NodeClass sets its own.
  configuration_values = each.value == "vpc-cni" ? jsonencode({
    env = { ENABLE_PREFIX_DELEGATION = "true", WARM_PREFIX_TARGET = "1" }
  }) : null
}

# CoreDNS needs a node to become active.
resource "aws_eks_addon" "coredns" {
  cluster_name                = aws_eks_cluster.lab.name
  addon_name                  = "coredns"
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "OVERWRITE"

  configuration_values = jsonencode({
    replicaCount = 1
    tolerations  = [{ key = "CriticalAddonsOnly", operator = "Exists" }]
  })

  depends_on = [aws_eks_node_group.system]
}
