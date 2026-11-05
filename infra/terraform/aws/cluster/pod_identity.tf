# EKS Pod Identity: a role trusted by pods.eks.amazonaws.com plus an
# association that binds it to one service account. The AWS Load Balancer
# Controller is the first example; Karpenter's controller uses the same
# mechanism through the karpenter module.
data "aws_iam_policy_document" "pod_identity_trust" {
  statement {
    actions = ["sts:AssumeRole", "sts:TagSession"]
    principals {
      type        = "Service"
      identifiers = ["pods.eks.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [local.account]
    }
  }
}

# Upstream policy for aws-load-balancer-controller v3.6.2
# (docs/install/iam_policy.json), unchanged.
resource "aws_iam_policy" "lbc" {
  name   = "${var.cluster_name}-aws-load-balancer-controller"
  policy = file("${path.module}/lbc-iam-policy.json")
}

resource "aws_iam_role" "lbc" {
  name               = "${var.cluster_name}-aws-load-balancer-controller"
  assume_role_policy = data.aws_iam_policy_document.pod_identity_trust.json
}

resource "aws_iam_role_policy_attachment" "lbc" {
  role       = aws_iam_role.lbc.name
  policy_arn = aws_iam_policy.lbc.arn
}

resource "aws_eks_pod_identity_association" "lbc" {
  cluster_name    = aws_eks_cluster.lab.name
  namespace       = "kube-system"
  service_account = "aws-load-balancer-controller"
  role_arn        = aws_iam_role.lbc.arn

  depends_on = [aws_eks_addon.before_nodes]
}

# The EBS CSI driver's controller (lab installs it with Helm, like the Load
# Balancer Controller): gp3 volumes for Postgres and Prometheus. The
# managed policy lets it create and delete volumes; lab gives every volume
# the lab tags (extraVolumeTags), so the reaper and the leftovers check
# see any it leaves behind.
resource "aws_iam_role" "ebs_csi" {
  name               = "${var.cluster_name}-ebs-csi"
  assume_role_policy = data.aws_iam_policy_document.pod_identity_trust.json
}

resource "aws_iam_role_policy_attachment" "ebs_csi" {
  role       = aws_iam_role.ebs_csi.name
  policy_arn = "${local.arn}:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicyV2"
}

resource "aws_eks_pod_identity_association" "ebs_csi" {
  cluster_name    = aws_eks_cluster.lab.name
  namespace       = "kube-system"
  service_account = "ebs-csi-controller-sa"
  role_arn        = aws_iam_role.ebs_csi.arn

  depends_on = [aws_eks_addon.before_nodes]
}

# External Secrets Operator (the secrets stack) reads Secrets Manager with
# this role: the ClusterSecretStore lab-secrets names no credentials, so
# ESO uses the pod's. Read only, and only the lab's secrets: lab/* (the
# sample-db stack) and lab??/* (labNN/..., the labs).
data "aws_iam_policy_document" "eso" {
  statement {
    actions = [
      "secretsmanager:GetSecretValue",
      "secretsmanager:DescribeSecret",
      "secretsmanager:ListSecretVersionIds",
    ]
    resources = [
      "${local.arn}:secretsmanager:${var.region}:${local.account}:secret:lab/*",
      "${local.arn}:secretsmanager:${var.region}:${local.account}:secret:lab??/*",
    ]
  }
}

resource "aws_iam_role" "eso" {
  name               = "${var.cluster_name}-external-secrets"
  assume_role_policy = data.aws_iam_policy_document.pod_identity_trust.json
}

resource "aws_iam_role_policy" "eso" {
  name   = "read-lab-secrets"
  role   = aws_iam_role.eso.name
  policy = data.aws_iam_policy_document.eso.json
}

resource "aws_eks_pod_identity_association" "eso" {
  cluster_name    = aws_eks_cluster.lab.name
  namespace       = "external-secrets"
  service_account = "external-secrets"
  role_arn        = aws_iam_role.eso.arn

  depends_on = [aws_eks_addon.before_nodes]
}
