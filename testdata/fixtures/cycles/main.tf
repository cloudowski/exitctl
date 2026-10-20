terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = "eu-central-1"
}

# Two security groups that reference each other: a true 2-cycle in the
# dependency graph (a depends on b and b depends on a).
resource "aws_security_group" "a" {
  name        = "cycles-a"
  description = "group a"

  tags = {
    Name = "cycles-a"
    Peer = aws_security_group.b.id
  }
}

resource "aws_security_group" "b" {
  name        = "cycles-b"
  description = "group b"

  tags = {
    Name = "cycles-b"
    Peer = aws_security_group.a.id
  }
}

# Rules that open traffic between the groups. Each rule depends on both
# groups, so the rules sit one wave above the cycle.
resource "aws_security_group_rule" "a_from_b" {
  type                     = "ingress"
  from_port                = 443
  to_port                  = 443
  protocol                 = "tcp"
  security_group_id        = aws_security_group.a.id
  source_security_group_id = aws_security_group.b.id
}

resource "aws_security_group_rule" "b_from_a" {
  type                     = "ingress"
  from_port                = 443
  to_port                  = 443
  protocol                 = "tcp"
  security_group_id        = aws_security_group.b.id
  source_security_group_id = aws_security_group.a.id
}
