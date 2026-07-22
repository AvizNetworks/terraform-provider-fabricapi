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
  #   FABRIC_API_ENDPOINT
  #   FABRIC_NAME
}

resource "fabricapi_gpu_allocations" "this" {
  tenant_name = var.tenant_name
  fabric_name = var.tenant_fabric
  operation   = var.operation

  allocations = [
    for a in var.allocations : {
      suid   = a.suid
      server = a.server
      gpus   = a.gpus
    }
  ]

  prefer           = var.prefer
  webhooks_enabled = var.webhooks_enabled
  webhook_url      = var.webhook_url
  webhook_events   = var.webhook_events
}

output "tenant_name" {
  value = var.tenant_name
}

output "gpu_allocations_operation_id" {
  value = fabricapi_gpu_allocations.this.operation_id
}

output "gpu_allocations_id" {
  value = fabricapi_gpu_allocations.this.id
}
