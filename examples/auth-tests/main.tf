terraform {
  required_version = ">= 1.5.0"

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
  #   FABRIC_API_AUTH_ENDPOINT
  # Auth options (pick one):
  #   - username/password: FABRIC_API_USERNAME / FABRIC_API_PASSWORD
  #   - access token:      FABRIC_API_ACCESS_TOKEN
  #   - refresh token:     FABRIC_API_REFRESH_TOKEN (used when access token expires / 401 refresh)
}

data "fabricapi_tenants" "this" {}

output "tenants" {
  value = data.fabricapi_tenants.this.tenants
}

output "tenant_count" {
  value = length(data.fabricapi_tenants.this.tenants)
}

