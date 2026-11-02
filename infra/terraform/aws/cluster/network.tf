# Two public subnets in two AZs and no NAT gateway: nodes get public IPv4
# addresses for egress, and nothing on the internet can reach them (the
# security groups below allow no inbound traffic from outside the VPC).
resource "aws_vpc" "lab" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = { Name = var.cluster_name }
}

resource "aws_internet_gateway" "lab" {
  vpc_id = aws_vpc.lab.id
  tags   = { Name = var.cluster_name }
}

resource "aws_subnet" "public" {
  count                   = length(local.azs)
  vpc_id                  = aws_vpc.lab.id
  availability_zone       = local.azs[count.index]
  cidr_block              = cidrsubnet(var.vpc_cidr, 4, count.index)
  map_public_ip_on_launch = true

  tags = merge(local.discovery, {
    Name = "${var.cluster_name}-public-${local.azs[count.index]}"
    # The Load Balancer Controller places internet-facing load balancers here.
    "kubernetes.io/role/elb" = "1"
  })
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.lab.id
  tags   = { Name = "${var.cluster_name}-public" }
}

resource "aws_route" "internet" {
  route_table_id         = aws_route_table.public.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.lab.id
}

resource "aws_route_table_association" "public" {
  count          = length(aws_subnet.public)
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# The VPC's default security group gets no rules at all, so anything that
# lands in it by accident can talk to nothing.
resource "aws_default_security_group" "lab" {
  vpc_id = aws_vpc.lab.id
  tags   = { Name = "${var.cluster_name}-default-deny" }
}

# Nodes (system node group and Karpenter) use the cluster security group
# that EKS creates: it allows traffic only between members of itself, so the
# control plane and nodes reach each other and nothing else can come in.
# Load balancers get their own groups from the Load Balancer Controller.
# This tag lets Karpenter's EC2NodeClass select that group.
resource "aws_ec2_tag" "cluster_sg_discovery" {
  resource_id = aws_eks_cluster.lab.vpc_config[0].cluster_security_group_id
  key         = "karpenter.sh/discovery"
  value       = var.cluster_name
}
