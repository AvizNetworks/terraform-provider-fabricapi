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

# CIDRPool manifests for the fabric's East-West rails (SpectrumX fabrics with L3 E-W links).
data "fabricapi_rail_cidr_pools" "all" {
  fabric_name = var.fabric_name != "" ? var.fabric_name : null
}

data "fabricapi_rail_cidr_pool" "one" {
  count       = var.rail_name != "" ? 1 : 0
  fabric_name = data.fabricapi_rail_cidr_pools.all.fabric_name
  rail_name   = var.rail_name
}

output "rails" {
  value = data.fabricapi_rail_cidr_pools.all.rails
}

# Save for kubectl apply with: terraform output -raw manifest > rails-cidrpools.yaml
output "manifest" {
  value = data.fabricapi_rail_cidr_pools.all.yaml
}

output "rail_manifest" {
  value = var.rail_name != "" ? data.fabricapi_rail_cidr_pool.one[0].yaml : null
}
