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

# Global registration: matching events from every fabric/tenant, optionally filtered.
resource "fabricapi_webhook" "global" {
  callback_url        = var.callback_url
  event_types         = var.event_types
  enabled             = var.enabled
  filter_fabric_names = var.filter_fabric_names
  filter_tenant_names = var.filter_tenant_names
}

# Fabric registration (at most one per fabric): fabric.deploy / fabric.delete.
resource "fabricapi_fabric_webhook" "this" {
  count = var.fabric_callback_url != "" ? 1 : 0

  fabric_name  = var.fabric_name != "" ? var.fabric_name : null
  callback_url = var.fabric_callback_url
  event_types  = ["fabric.deploy", "fabric.delete"]
}

# Tenant registration (at most one per tenant): tenant.* events.
# Don't also enable per-request webhooks on fabricapi_tenant for the same tenant.
resource "fabricapi_tenant_webhook" "this" {
  count = var.tenant_name != "" && var.tenant_callback_url != "" ? 1 : 0

  fabric_name  = var.fabric_name != "" ? var.fabric_name : null
  tenant_name  = var.tenant_name
  callback_url = var.tenant_callback_url
}

data "fabricapi_webhooks" "all" {
  depends_on = [fabricapi_webhook.global]
}

output "global_webhook_id" {
  value = fabricapi_webhook.global.webhook_id
}

output "global_webhooks" {
  value = data.fabricapi_webhooks.all.webhooks
}
