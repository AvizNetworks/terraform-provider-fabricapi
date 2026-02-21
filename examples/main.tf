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

# Create or manage a tenant (only if manage_tenant is true)
resource "fabricapi_tenant" "example" {
  count            = var.manage_tenant ? 1 : 0
  tenant_name      = var.tenant_name
  description      = var.tenant_description
  max_gpus_allowed = var.max_gpus_allowed
}

# Manage servers for tenant
# If manage_tenant is true, depends on tenant resource
# If manage_tenant is false, operates on existing tenant
resource "fabricapi_tenant_servers" "example_servers" {
  count       = length(var.servers) > 0 ? 1 : 0
  tenant_name = var.manage_tenant && length(fabricapi_tenant.example) > 0 ? fabricapi_tenant.example[0].tenant_name : var.tenant_name
  operation   = var.operation
  servers     = var.servers

  # Always include dependency - if tenant resource doesn't exist (count=0), this is ignored
  depends_on = [fabricapi_tenant.example]
  
  lifecycle {
    # Ensure servers are destroyed before tenant during terraform destroy
    create_before_destroy = false
  }
}

# Output values
output "tenant_name" {
  value = var.manage_tenant ? (length(fabricapi_tenant.example) > 0 ? fabricapi_tenant.example[0].tenant_name : var.tenant_name) : var.tenant_name
}

output "tenant_id" {
  value = var.manage_tenant ? (length(fabricapi_tenant.example) > 0 ? fabricapi_tenant.example[0].id : null) : null
}

output "servers_operation_id" {
  value = length(fabricapi_tenant_servers.example_servers) > 0 ? fabricapi_tenant_servers.example_servers[0].id : null
}

output "tenant_names" {
  value = data.fabricapi_tenants.list.tenants
}


data "fabricapi_tenants" "list" {
  # no arguments needed
}
