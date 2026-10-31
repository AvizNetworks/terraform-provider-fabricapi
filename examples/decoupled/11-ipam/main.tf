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
  #   FABRIC_API_ENDPOINT  (Fabric Manager, e.g. https://<ones-host>:8443)
  #   FABRIC_API_AUTH_ENDPOINT, FABRIC_NAME
}

# Turns IPAM on for the fabric (External tenant control only). Destroy turns it off.
# Changing mode replaces the resource (off, then on in the new mode).
resource "fabricapi_ipam_config" "this" {
  fabric_name        = var.fabric_name != "" ? var.fabric_name : null
  mode               = var.mode
  enable_ip_assign   = var.mode == "static" ? var.enable_ip_assign : null
  lease_time_seconds = var.mode == "dhcp" ? var.lease_time_seconds : null
  force              = var.force
}

data "fabricapi_ipam_config" "this" {
  fabric_name = fabricapi_ipam_config.this.fabric_name
}

# Address lookups — only once tenants exist and servers are allotted to them.
data "fabricapi_tenant_ip_addresses" "tenant" {
  count       = var.tenant_name != "" ? 1 : 0
  fabric_name = fabricapi_ipam_config.this.fabric_name
  tenant_name = var.tenant_name
}

data "fabricapi_server_ip_addresses" "server" {
  count       = var.server_name != "" ? 1 : 0
  fabric_name = fabricapi_ipam_config.this.fabric_name
  server_name = var.server_name
}

data "fabricapi_tenant_compute_ip" "this" {
  count       = var.tenant_name != "" && var.server_name != "" ? 1 : 0
  fabric_name = fabricapi_ipam_config.this.fabric_name
  tenant_name = var.tenant_name
  server_name = var.server_name
}

output "ipam_config" {
  value = data.fabricapi_ipam_config.this
}

output "tenant_pool" {
  value = var.tenant_name != "" ? {
    cidr        = data.fabricapi_tenant_ip_addresses.tenant[0].pool_cidr
    allocated   = data.fabricapi_tenant_ip_addresses.tenant[0].allocated_count
    allocations = data.fabricapi_tenant_ip_addresses.tenant[0].allocations
  } : null
}

output "server_allocations" {
  value = var.server_name != "" ? data.fabricapi_server_ip_addresses.server[0].allocations : null
}

output "tenant_compute_ip" {
  value = length(data.fabricapi_tenant_compute_ip.this) > 0 ? data.fabricapi_tenant_compute_ip.this[0].ip_address : null
}
