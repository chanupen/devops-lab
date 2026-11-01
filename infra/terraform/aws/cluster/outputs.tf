output "cluster_name" {
  value = aws_eks_cluster.lab.name
}

output "cluster_endpoint" {
  value = aws_eks_cluster.lab.endpoint
}

output "cluster_ca" {
  description = "Base64 cluster CA certificate, for the kubeconfig lab writes."
  value       = aws_eks_cluster.lab.certificate_authority[0].data
}

output "vpc_id" {
  value = aws_vpc.lab.id
}

output "karpenter_queue" {
  value = module.karpenter.queue_name
}

output "karpenter_node_role" {
  value = module.karpenter.node_iam_role_name
}

output "lbc_role_arn" {
  value = aws_iam_role.lbc.arn
}
