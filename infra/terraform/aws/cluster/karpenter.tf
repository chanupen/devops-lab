# The AWS side of Karpenter: controller role (through Pod Identity), node
# role with its EKS access entry, and the SQS queue plus EventBridge rules
# for spot interruption notices. lab installs the Helm chart, the
# EC2NodeClass and the NodePool after this apply.
module "karpenter" {
  source  = "terraform-aws-modules/eks/aws//modules/karpenter"
  version = "~> 21.0"

  cluster_name = aws_eks_cluster.lab.name
  namespace    = "kube-system"

  create_pod_identity_association = true

  # The module's node role already has the worker, CNI and ECR pull-only
  # policies, so nodes can pull from the lab's ECR repository.
  node_iam_role_use_name_prefix = false
  node_iam_role_name            = "${var.cluster_name}-karpenter-node"

  iam_role_use_name_prefix = false
  iam_role_name            = "${var.cluster_name}-karpenter-controller"
  queue_name               = "${var.cluster_name}-karpenter"
  rule_name_prefix         = "${var.cluster_name}-"
}
