terraform {
  required_version = ">= 1.5.0"

  required_providers {
    fabricapi = {
      source  = "local/fabricapi"
      version = "1.0.0"
    }
  }
}

provider "fabricapi" {
  # Prefer environment variables (AWS-style):
  #   FABRIC_API_ENDPOINT
  #   FABRIC_NAME
}

locals {
  legacy_tenants = var.tenant_name == "" ? {} : {
    (var.tenant_name) = {
      # tenant_description is optional in legacy mode. If omitted, generate a stable,
      # non-empty value so create works and destroy does not require extra flags.
      description      = length(trimspace(var.tenant_description)) > 0 ? var.tenant_description : "Terraform tenant ${var.tenant_name}"
      max_gpus_allowed = var.max_gpus_allowed
      prefer           = var.prefer
      webhooks_enabled = var.webhooks_enabled
      webhook_url      = var.webhook_url
      webhook_events   = length(var.webhook_events) > 0 ? var.webhook_events : null
    }
  }

  tenants_effective = length(var.tenants) > 0 ? var.tenants : local.legacy_tenants
}

check "tenants_input" {
  assert {
    condition     = length(local.tenants_effective) > 0
    error_message = "Define var.tenants (non-empty map) or legacy var.tenant_name."
  }

  assert {
    condition     = !(length(var.tenants) > 0 && var.tenant_name != "")
    error_message = "Do not set both var.tenants and legacy var.tenant_name; use one mode."
  }

  # tenant_description is optional; a default is generated when omitted.
}

resource "fabricapi_tenant" "this" {
  for_each = local.tenants_effective

  tenant_name      = each.key
  description      = each.value.description
  max_gpus_allowed = each.value.max_gpus_allowed
  prefer           = each.value.prefer
  webhooks_enabled = each.value.webhooks_enabled
  webhook_url      = try(each.value.webhook_url, null)
  webhook_events   = try(each.value.webhook_events, null)
}

# Multi-tenant outputs
output "tenants_by_name" {
  description = "All managed tenants: name => { tenant_name, id, operation_id }"
  value = {
    for k, t in fabricapi_tenant.this : k => {
      tenant_name      = t.tenant_name
      id               = t.id
      operation_id     = t.operation_id
      prefer           = t.prefer
      webhooks_enabled = t.webhooks_enabled
    }
  }
}

# Backward-compatible outputs when exactly one tenant is managed
output "tenant_name" {
  description = "Set only when exactly one tenant exists in state."
  value       = length(fabricapi_tenant.this) == 1 ? fabricapi_tenant.this[sort(keys(fabricapi_tenant.this))[0]].tenant_name : null
}

output "tenant_id" {
  description = "Set only when exactly one tenant exists in state."
  value       = length(fabricapi_tenant.this) == 1 ? fabricapi_tenant.this[sort(keys(fabricapi_tenant.this))[0]].id : null
}

output "tenant_operation_id" {
  description = "Set only when exactly one tenant exists in state."
  value       = length(fabricapi_tenant.this) == 1 ? fabricapi_tenant.this[sort(keys(fabricapi_tenant.this))[0]].operation_id : null
}
