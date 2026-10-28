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

# Allows traffic from a source (a whole tenant, or service endpoints) to a destination.
# Fabric Manager pushes the config to the devices before answering. Adding endpoints
# updates the policy in place; removing any (or changing a side) replaces it.
resource "fabricapi_network_policy" "this" {
  fabric_name = var.fabric_name != "" ? var.fabric_name : null
  name        = var.policy_name

  source = {
    type        = var.source_type
    tenant_name = var.source_tenant
    endpoints   = length(var.source_endpoints) > 0 ? var.source_endpoints : null
  }

  destination = {
    type         = var.destination_type
    tenant_name  = var.destination_tenant
    service_name = var.service_name != "" ? var.service_name : null
    endpoints    = length(var.destination_endpoints) > 0 ? var.destination_endpoints : null
  }
}

data "fabricapi_network_policies" "all" {
  fabric_name = fabricapi_network_policy.this.fabric_name
}

output "policy_id" {
  value = fabricapi_network_policy.this.id
}

output "all_policies" {
  value = data.fabricapi_network_policies.all.policies
}
