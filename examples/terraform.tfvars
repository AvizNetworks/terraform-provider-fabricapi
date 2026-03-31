# Example values - users can modify these

# API Configuration
api_endpoint = "http://localhost:8787"

fabric_name = "1SU-Fabric170619"

# Tenant Configuration
tenant_name        = "madhu01"
tenant_description = "TF Test tenant for GPU workloads"
# Actual GPUs allocated = len(servers) * 8 (each host contributes 8 GPUs).
# max_gpus_allowed is a tenant cap; it must be >= len(servers)*8.
# Maximum supported: 32 GPUs = 4 hosts (8 + 16 + 24 + 32 GPU tiers).
max_gpus_allowed = 32

# All hosts in this fabric unit (maximum capacity: 4 servers × 8 GPUs = 32 GPUs)
servers = [
  "hgx-su00-h00",
  "hgx-su00-h01",
  "hgx-su00-h02",
  "hgx-su00-h03",
]

# Operation: "ADD" to allocate GPUs, "DELETE" or "REMOVE" to deallocate GPUs
operation = "ADD"

# Optional shared flag in PATCH payload. Set true/false to send explicit value.
# Set to null to omit `shared` from the payload.
shared = true

# Set to true to create/manage the tenant, false to only manage servers for existing tenant
manage_tenant = true

# Set to true if importing an existing tenant
import_existing_tenant = false

# VPC peering after GPU allocation (defaults: on, same fabric as fabric_name)
create_vpcpeering        = true
vpcpeering_target_fabric = "1SU-Fabric170619"
# vpcpeering_name defaults to "tf-vpcpeering" in variables.tf — set a unique name if re-applying
