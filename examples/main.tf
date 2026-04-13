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

# Single place for values reused across resources; optional vars can override.
locals {
  tenant_description_input = try(trimspace(var.tenant_description), "")

  tenant_description_effective = local.tenant_description_input != "" ? local.tenant_description_input : format(
    "Tenant %s on fabric %s (max %d GPUs)",
    var.tenant_name,
    var.fabric_name,
    var.max_gpus_allowed,
  )

  vpcpeering_target_fabric_effective = coalesce(var.vpcpeering_target_fabric, var.fabric_name)
}

# Create or manage a tenant (only if manage_tenant is true)
resource "fabricapi_tenant" "example" {
  count            = var.manage_tenant ? 1 : 0
  tenant_name      = var.tenant_name
  description      = local.tenant_description_effective
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
  shared      = var.shared

  # Always include dependency - if tenant resource doesn't exist (count=0), this is ignored
  depends_on = [fabricapi_tenant.example]

  lifecycle {
    # Ensure servers are destroyed before tenant during terraform destroy
    create_before_destroy = false

    # Enforce cross-variable constraint without using variable validation (which is limited).
    precondition {
      condition     = length(var.servers) == 0 || (length(var.servers) * 8) <= var.max_gpus_allowed
      error_message = "When servers are provided, server count must fit within max_gpus_allowed (8 GPUs per server)."
    }
  }
}

# Create VPC peering after tenant + GPU allocation
resource "fabricapi_vpcpeering" "example_vpcpeering" {
  count = var.create_vpcpeering ? 1 : 0

  tenant_name   = var.tenant_name
  target_fabric = local.vpcpeering_target_fabric_effective
  name          = var.vpcpeering_name

  delete_on_destroy = var.vpcpeering_delete_on_destroy

  # Ensure this only runs after server assignment.
  depends_on = [fabricapi_tenant_servers.example_servers]
}

# Output values
output "effective_tenant_description" {
  description = "Description sent to the tenant API (from tenant_description or auto-generated)."
  value       = local.tenant_description_effective
}

output "tenant_name" {
  value = var.manage_tenant ? (length(fabricapi_tenant.example) > 0 ? fabricapi_tenant.example[0].tenant_name : var.tenant_name) : var.tenant_name
}

output "tenant_id" {
  value = var.manage_tenant ? (length(fabricapi_tenant.example) > 0 ? fabricapi_tenant.example[0].id : null) : null
}

output "servers_operation_id" {
  value = length(fabricapi_tenant_servers.example_servers) > 0 ? fabricapi_tenant_servers.example_servers[0].id : null
}

# Expected GPUs from server list (8 per server). UI may show one row per server (e.g. two rows of 8/8 = 16 total).
output "expected_gpus_from_servers" {
  value = length(var.servers) * 8
}

output "vpcpeering_id" {
  value = length(fabricapi_vpcpeering.example_vpcpeering) > 0 ? fabricapi_vpcpeering.example_vpcpeering[0].id : null
}

