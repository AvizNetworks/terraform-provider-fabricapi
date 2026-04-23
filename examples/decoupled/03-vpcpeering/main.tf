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

  delete_on_destroy = var.delete_on_destroy
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

