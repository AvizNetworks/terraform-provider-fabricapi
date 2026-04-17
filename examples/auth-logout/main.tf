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
  # Uses env vars:
  #   FABRIC_API_ENDPOINT
  #   FABRIC_API_AUTH_ENDPOINT
  #   FABRIC_NAME
  #   FABRIC_API_REFRESH_TOKEN (or set refresh_token in provider config)
}

resource "fabricapi_auth_logout" "this" {}

