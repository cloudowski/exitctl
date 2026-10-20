terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.40"
    }
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.110"
    }
    oci = {
      source  = "oracle/oci"
      version = "~> 6.0"
    }
  }
}

provider "google" {
  project = "shop-prod"
  region  = "europe-west1"
}

provider "azurerm" {
  features {}
}

provider "oci" {
  region = "eu-frankfurt-1"
}
