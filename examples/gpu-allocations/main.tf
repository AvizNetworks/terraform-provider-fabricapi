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
  #   FABRIC_API_ACCESS_TOKEN
  #   FABRICAPI_INSECURE_TLS
}

resource "fabricapi_gpu_allocations" "gpus" {
  tenant_name = var.tenant_name
  operation   = var.operation

  servers = var.servers
}
