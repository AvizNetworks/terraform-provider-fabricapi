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
  #   FABRIC_API_ENDPOINT  (Fabric Manager, e.g. https://<ones-host>:8443)
  #   FABRIC_API_AUTH_ENDPOINT, FABRIC_NAME
}

# Read-only: lists the fabric's network policies without creating one.
data "fabricapi_network_policies" "all" {
  fabric_name = var.fabric_name != "" ? var.fabric_name : null
}

output "policies" {
  value = data.fabricapi_network_policies.all.policies
}
