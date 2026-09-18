terraform {
  required_providers {
    fabricapi = {
      source  = "local/fabricapi"
      version = "1.0.0"
    }
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
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
  host_map            = length(var.host_map) > 0 ? var.host_map : null
  starting_subnet_gpu = var.starting_subnet_gpu
  enable_ew           = var.enable_ew
  hosts_per_su        = var.hosts_per_su
  tenant_ctrl         = var.tenant_ctrl

  # North-South (front-end user/storage) networking — all optional, off by default.
  enable_ns               = var.enable_ns
  dedicated_storage       = var.dedicated_storage
  starting_subnet_cpu     = var.starting_subnet_cpu != "" ? var.starting_subnet_cpu : null
  starting_subnet_storage = var.starting_subnet_storage != "" ? var.starting_subnet_storage : null
  starting_subnet_tenants = var.starting_subnet_tenants != "" ? var.starting_subnet_tenants : null
}

# devices can come from a JSON file (var.devices_file) instead of inline HCL — handy when
# the credential list already exists as a file (e.g. exported from a lab topology tool).
# The file takes precedence over var.devices when set.
locals {
  devices_effective = var.devices_file != "" ? jsondecode(file(var.devices_file)) : var.devices

  # Scenario 3/4 support: a hand-edited or externally-sourced YAML overrides the server's
  # own generated YAML at deploy time. Empty string means "use the server's current YAML".
  custom_yaml_effective = var.custom_yaml_path != "" ? file(var.custom_yaml_path) : ""
}

# Scenario 2 — "design a fabric and view the YAML": always fetched (read-only, no side
# effects), so `terraform apply`/`plan` lets you inspect the generated topology at any time,
# whether or not you ever deploy. Save it locally for review/hand-editing with:
#   terraform output -raw generated_yaml > fabric.review.yaml
data "fabricapi_fabric_yaml" "this" {
  fabric_name = fabricapi_fabric.this.name
}

resource "local_file" "generated_yaml" {
  filename = "${path.module}/fabric.generated.yaml"
  content  = data.fabricapi_fabric_yaml.this.yaml
}

# Deploy is a separate, explicit step (opt-in via var.deploy) — it pushes the fabric's
# generated (or hand-edited, via var.custom_yaml_path) config onto real switches. Leave
# var.deploy=false to only design/review the fabric (Draft), same as before this resource
# existed.
resource "fabricapi_fabric_deploy" "this" {
  count = var.deploy ? 1 : 0

  fabric_name     = fabricapi_fabric.this.name
  description     = var.description
  deployment_type = var.deployment_type
  devices         = local.devices_effective
  custom_yaml     = local.custom_yaml_effective != "" ? local.custom_yaml_effective : null
}

output "fabric_id" {
  value = fabricapi_fabric.this.id
}

output "generated_yaml" {
  description = "The fabric's current generated YAML — download/review with: terraform output -raw generated_yaml > fabric.review.yaml"
  value       = data.fabricapi_fabric_yaml.this.yaml
}

output "fabric_deploy_id" {
  value = var.deploy ? fabricapi_fabric_deploy.this[0].id : null
}
