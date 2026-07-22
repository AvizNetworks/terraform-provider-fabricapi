terraform {
  required_providers {
    fabricapi = {
      source  = "local/fabricapi"
      version = "1.0.0"
    }
  }
}

provider "fabricapi" {
  # Prefer environment variables:
  #   FABRIC_API_ENDPOINT
  #   FABRIC_NAME
}

data "fabricapi_available_servers" "this" {
  fabric_name = var.fabric_name
}

output "fabric_name" {
  value = data.fabricapi_available_servers.this.fabric_name
}

output "available_gpus" {
  value = data.fabricapi_available_servers.this.available_gpus
}
