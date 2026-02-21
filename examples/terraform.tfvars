# Example values - users can modify these

# API Configuration
api_endpoint = "http://worker07.air.nvidia.com:22886"
fabric_name  = "fabi"

# Tenant Configuration
tenant_name        = "tf_tenant1"
tenant_description = "TF Test tenant for GPU workloads"
max_gpus_allowed   = 16  # Valid values: 8, 16, 24, 32

# Server Management
servers = ["hgx-su01-h00", "hgx-su01-h01"]  # Can use 1-4 servers in any combination  # Valid: hgx-su00-h00, hgx-su00-h01, hgx-su01-h00, hgx-su01-h01

# Operation: "ADD" to allocate GPUs, "DELETE" or "REMOVE" to deallocate GPUs
operation = "ADD"

# Set to true to create/manage the tenant, false to only manage servers for existing tenant
manage_tenant = true

# Set to true if importing an existing tenant
import_existing_tenant = false

