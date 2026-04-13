variable "api_endpoint" {
  description = "API endpoint URL. For day-to-day use, set once in fabric.identity.auto.tfvars (see fabric.identity.auto.tfvars.example)."
  type        = string
  default     = "http://localhost:8787"
}

variable "fabric_name" {
  description = "Fabric name. Prefer fabric.identity.auto.tfvars so you do not repeat on every command."
  type        = string
  default     = "1SU-Fabric172202"
}

variable "tenant_name" {
  description = "Tenant name. Prefer fabric.identity.auto.tfvars so you do not repeat on every command."
  type        = string
  validation {
    condition     = length(var.tenant_name) > 0
    error_message = "Tenant name cannot be empty."
  }
}

variable "tenant_description" {
  description = <<-EOT
    Optional tenant description for POST /fabrics/{fabric}/tenants.
    If null or blank, a description is generated from tenant_name, fabric_name, and max_gpus_allowed.
  EOT
  type        = string
  default     = null
  nullable    = true
}

variable "max_gpus_allowed" {
  description = <<-EOT
    Maximum number of GPUs allowed for the tenant.
    Valid values: 8, 16, 24, or 32
    
    Server count limits:
    - 8 GPUs  = max 1 server
    - 16 GPUs = max 2 servers
    - 24 GPUs = max 3 servers
    - 32 GPUs = max 4 servers
  EOT
  type        = number
  default     = 32
  validation {
    condition     = contains([8, 16, 24, 32], var.max_gpus_allowed)
    error_message = "Max GPUs must be a multiple of 8: valid values are 8, 16, 24, or 32."
  }
}

variable "servers" {
  description = <<-EOT
    List of servers to allocate GPUs from.
    
    Server IDs (varies by deployment).
    
    Server count must fit within max_gpus_allowed:
    - 8 GPUs  = 1 server max
    - 16 GPUs = 2 servers max
    - 24 GPUs = 3 servers max
    - 32 GPUs = 4 servers max
  EOT
  type        = list(string)
  default     = []
  validation {
    # Don't hardcode server names here; real Fabric deployments may use different server IDs.
    # Variable validation blocks can only reference the variable being validated (var.servers),
    # so cross-variable checks (against var.max_gpus_allowed) are enforced elsewhere.
    condition     = length(var.servers) == 0 || (length(var.servers) >= 1 && length(var.servers) <= 4)
    error_message = "When servers are provided, server count must be 1-4."
  }
}

variable "operation" {
  description = "Operation for server management: ADD, DELETE, or REMOVE (REMOVE is alias for DELETE)"
  type        = string
  default     = "ADD"
  validation {
    condition     = contains(["ADD", "DELETE", "REMOVE"], var.operation)
    error_message = "Operation must be either ADD, DELETE, or REMOVE."
  }
}

variable "shared" {
  description = <<-EOT
    Shared GPU allocation flag for PATCH /fabrics/{fabric}/tenants/{tenant} (per server in JSON).
    Must match API expectations: same as curl body
    {"operation":"ADD","servers":[{"serverName":"...","shared":true}]}.
    When true/false, the provider sends that value for every server. When null (omit in .tfvars),
    the provider omits `shared` from each server object (API may treat differently).
  EOT
  type        = bool
  default     = true
}

variable "manage_tenant" {
  description = "Whether to manage the tenant resource (create/update/delete). Set to false to only manage server assignments for existing tenants."
  type        = bool
  default     = true
}

variable "import_existing_tenant" {
  description = "Set to true if you want to import an existing tenant into Terraform state"
  type        = bool
  default     = false
}

variable "create_vpcpeering" {
  description = "If true, create a VPC peering after tenant + GPU allocation (default: on)."
  type        = bool
  default     = true
}

variable "vpcpeering_target_fabric" {
  description = <<-EOT
    Fabric for POST /fabrics/{target_fabric}/vpcpeering.
    If null, uses fabric_name (same fabric as the provider default).
  EOT
  type        = string
  default     = null
  nullable    = true
}

variable "vpcpeering_name" {
  description = "Name for the VPC peering object."
  type        = string
  default     = "tf-vpcpeering"
}

variable "vpcpeering_delete_on_destroy" {
  description = "If true, attempt to delete vpcpeering on destroy (not implemented by this provider yet)."
  type        = bool
  default     = false
}