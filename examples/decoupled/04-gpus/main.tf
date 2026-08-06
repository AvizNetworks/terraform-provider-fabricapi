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

resource "fabricapi_tenant_gpus" "this" {
  tenant_name  = var.tenant_name
  fabric_name  = var.tenant_fabric
  operation    = var.operation
  server_names = var.server_names
  gpu_ids      = length(var.gpu_ids) > 0 ? var.gpu_ids : null
  membership   = var.membership != "" ? var.membership : null
}

output "tenant_name" {
  value = var.tenant_name
}

output "gpus_id" {
  value = fabricapi_tenant_gpus.this.id
}
