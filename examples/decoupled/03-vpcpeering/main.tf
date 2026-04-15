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

resource "fabricapi_vpcpeering" "this" {
  tenant_name = var.tenant_name
  name        = var.vpcpeering_name

  # Omit when empty so state stays null and matches FABRIC_NAME-only workflows.
  target_fabric     = var.fabric != "" ? var.fabric : null
  tenant_fabric     = var.fabric != "" ? var.fabric : null
  delete_on_destroy = var.delete_on_destroy

  prefer           = var.prefer
  webhooks_enabled = var.webhooks_enabled
  webhook_url      = var.webhook_url
  webhook_events   = var.webhook_events
}

output "tenant_name" {
  value = var.tenant_name
}

output "vpcpeering_id" {
  value = fabricapi_vpcpeering.this.id
}

output "vpcpeering_operation_id" {
  value = fabricapi_vpcpeering.this.operation_id
}

