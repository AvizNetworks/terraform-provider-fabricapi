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

resource "fabricapi_tenant" "this" {
  tenant_name      = var.tenant_name
  description      = var.tenant_description
  max_gpus_allowed = var.max_gpus_allowed
}

output "tenant_name" {
  value = fabricapi_tenant.this.tenant_name
}

output "tenant_id" {
  value = fabricapi_tenant.this.id
}

