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

# Tenant DHCP subnet, whether Kea is serving it, and its leases.
data "fabricapi_tenant_dhcp" "this" {
  fabric_name = var.fabric_name != "" ? var.fabric_name : null
  tenant_name = var.tenant_name
}

# Re-apply every tenant DHCP subnet to Kea. Runs on create and whenever
# reconcile_trigger changes (e.g. -var="reconcile_trigger=$(date +%s)").
resource "fabricapi_dhcp_reconcile" "this" {
  count       = var.reconcile_trigger != "" ? 1 : 0
  fabric_name = var.fabric_name != "" ? var.fabric_name : null
  triggers    = { run = var.reconcile_trigger }
}

# Retry one tenant's DHCP provisioning (fabric-wide re-apply, gated on the tenant).
resource "fabricapi_tenant_dhcp_retry" "this" {
  count       = var.retry_trigger != "" ? 1 : 0
  fabric_name = var.fabric_name != "" ? var.fabric_name : null
  tenant_name = var.tenant_name
  triggers    = { run = var.retry_trigger }
}

output "subnet" {
  value = data.fabricapi_tenant_dhcp.this.subnet
}

output "live_in_kea" {
  value = data.fabricapi_tenant_dhcp.this.live_in_kea
}

output "lease_count" {
  value = data.fabricapi_tenant_dhcp.this.leases == null ? null : length(data.fabricapi_tenant_dhcp.this.leases)
}

output "reconcile_summary" {
  value = length(fabricapi_dhcp_reconcile.this) > 0 ? fabricapi_dhcp_reconcile.this[0].summary : null
}

output "retry_summary" {
  value = length(fabricapi_tenant_dhcp_retry.this) > 0 ? fabricapi_tenant_dhcp_retry.this[0].summary : null
}
