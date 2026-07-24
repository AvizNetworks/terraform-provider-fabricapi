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

data "fabricapi_vf_interfaces" "this" {
  fabric_name = var.fabric_name
  server_name = var.server_name
}

output "fabric_name" {
  value = data.fabricapi_vf_interfaces.this.fabric_name
}

output "server_name" {
  value = data.fabricapi_vf_interfaces.this.server_name
}

output "dpu_count" {
  value = data.fabricapi_vf_interfaces.this.dpu_count
}

output "dpus" {
  value = data.fabricapi_vf_interfaces.this.dpus
}
