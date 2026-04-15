terraform {
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

resource "fabricapi_tenant_servers" "this" {
  tenant_name = var.tenant_name
  fabric_name = var.tenant_fabric
  operation   = var.operation
  servers     = var.servers
  shared      = var.shared

  prefer           = var.prefer
  webhooks_enabled = var.webhooks_enabled
  webhook_url      = var.webhook_url
  webhook_events   = var.webhook_events
}

output "tenant_name" {
  value = var.tenant_name
}

output "servers_operation_id" {
  value = fabricapi_tenant_servers.this.operation_id
}

