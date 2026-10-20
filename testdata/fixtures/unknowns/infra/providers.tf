terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
    acme = {
      source  = "acme-corp/acme"
      version = "~> 0.4"
    }
    exoticcloud = {
      source  = "exoticcloud/exoticcloud"
      version = "~> 1.2"
    }
  }
}

provider "aws" {
  region = "eu-west-1"
}
