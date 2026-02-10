terraform {
  required_providers {
    fabricapi = {
      source  = "local/fabricapi"
      version = "1.0.0"
    }
  }
}

provider "fabricapi" {
  endpoint = var.api_endpoint
  fabric   = var.fabric_name
}

# Create a tenant
resource "fabricapi_tenant" "example" {
  tenant_name      = var.tenant_name
  description      = var.tenant_description
  max_gpus_allowed = var.max_gpus_allowed
}

# Add servers to tenant (only if servers list is not empty)
resource "fabricapi_tenant_servers" "example_add" {
  count       = length(var.servers) > 0 ? 1 : 0
  tenant_name = fabricapi_tenant.example.tenant_name
  operation   = var.operation
  servers     = var.servers

  depends_on = [fabricapi_tenant.example]
}

# Output values
output "tenant_name" {
  value = fabricapi_tenant.example.tenant_name
}

output "tenant_id" {
  value = fabricapi_tenant.example.id
}