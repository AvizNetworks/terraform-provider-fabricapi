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

# devices can come from a JSON file (var.devices_file) instead of inline HCL — handy when
# the credential list already exists as a file (e.g. exported from a lab topology tool).
# The file takes precedence over var.devices when set.
locals {
  devices_effective = var.devices_file != "" ? jsondecode(file(var.devices_file)) : var.devices
}

# Deploy is a separate, explicit step (opt-in via var.deploy) — it pushes the fabric's
# generated config onto real switches. Leave var.deploy=false to only design/review the
# fabric (Draft), same as before this resource existed.
resource "fabricapi_fabric_deploy" "this" {
  count = var.deploy ? 1 : 0

  fabric_name     = fabricapi_fabric.this.name
  description     = var.description
  deployment_type = var.deployment_type
  devices         = local.devices_effective
}

output "fabric_id" {
  value = fabricapi_fabric.this.id
}

output "fabric_deploy_id" {
  value = var.deploy ? fabricapi_fabric_deploy.this[0].id : null
}
