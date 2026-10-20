# Remote registry module; .terraform/modules is deliberately absent.

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.0"

  name = "unknowns"
  cidr = "10.20.0.0/16"
}

resource "aws_security_group" "app" {
  name   = "app"
  vpc_id = module.vpc.vpc_id
}
