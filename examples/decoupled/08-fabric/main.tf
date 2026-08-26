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
  #   FABRIC_API_ENDPOINT, FABRIC_NAME
  #   FABRIC_API_CONFIG_ENDPOINT (separate ONES UI config service host)
}

resource "fabricapi_fabric" "this" {
  name                = var.name
  type                = var.type
  description         = var.description
  num_of_sus          = var.num_of_sus
  max_num_of_sus      = var.max_num_of_sus
  host_map            = var.host_map
  starting_subnet_gpu = var.starting_subnet_gpu
  simulation_id       = var.simulation_id
  enable_ew           = var.enable_ew
  su_host_cnt         = var.su_host_cnt
  tenant              = var.tenant
}

output "fabric_id" {
  value = fabricapi_fabric.this.id
}
