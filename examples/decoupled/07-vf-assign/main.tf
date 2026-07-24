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

resource "fabricapi_vf_assign" "this" {
  fabric_name = var.fabric_name
  server_name = var.server_name
  vf_id       = var.vf_id
  tenant_name = var.tenant_name
  prefer      = var.prefer
}

output "vf_assign_id" {
  value = fabricapi_vf_assign.this.id
}

output "status" {
  value = fabricapi_vf_assign.this.status
}

output "vlan_id" {
  value = fabricapi_vf_assign.this.vlan_id
}

output "vni_id" {
  value = fabricapi_vf_assign.this.vni_id
}

output "dpu_name" {
  value = fabricapi_vf_assign.this.dpu_name
}

output "if_name" {
  value = fabricapi_vf_assign.this.if_name
}

output "server_if" {
  value = fabricapi_vf_assign.this.server_if
}
