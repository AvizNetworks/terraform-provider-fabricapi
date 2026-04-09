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
  tenant_name   = var.tenant_name
  target_fabric = var.target_fabric
  name          = var.vpcpeering_name

  tenant_fabric = var.tenant_fabric
  delete_on_destroy = var.delete_on_destroy
}

output "tenant_name" {
  value = var.tenant_name
}

output "vpcpeering_id" {
  value = fabricapi_vpcpeering.this.id
}

