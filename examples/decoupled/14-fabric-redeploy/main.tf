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
  #   FABRIC_API_CONFIG_ENDPOINT
}

resource "fabricapi_fabric_redeploy" "this" {
  fabric_name       = var.fabric_name
  allow_unreachable = var.allow_unreachable
}

output "message" {
  value = fabricapi_fabric_redeploy.this.message
}
