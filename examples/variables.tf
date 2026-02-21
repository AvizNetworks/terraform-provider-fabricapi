variable "api_endpoint" {
  description = "API endpoint URL"
  type        = string
  default     = "http://worker07.air.nvidia.com:22886"
}

variable "fabric_name" {
  description = "Fabric name"
  type        = string
  default     = "fabi"
}

variable "tenant_name" {
  description = "Name of the tenant to create"
  type        = string
  validation {
    condition     = length(var.tenant_name) > 0
    error_message = "Tenant name cannot be empty."
  }
}

variable "tenant_description" {
  description = "Description of the tenant"
  type        = string
  default     = "Managed by Terraform"
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
  default     = 16
  validation {
    condition     = contains([8, 16, 24, 32], var.max_gpus_allowed)
    error_message = "Max GPUs must be a multiple of 8: valid values are 8, 16, 24, or 32."
  }
}

variable "servers" {
  description = <<-EOT
    List of servers to allocate GPUs from.
    
    Available servers:
    - hgx-su00-h00
    - hgx-su00-h01
    - hgx-su01-h00
    - hgx-su01-h01
    
    Server count must match max_gpus_allowed:
    - 8 GPUs  = 1 server max
    - 16 GPUs = 2 servers max
    - 24 GPUs = 3 servers max
    - 32 GPUs = 4 servers max
  EOT
  type        = list(string)
  default     = []
  validation {
    condition = alltrue([
      for server in var.servers : contains(
        ["hgx-su00-h00", "hgx-su00-h01", "hgx-su01-h00", "hgx-su01-h01"],
        server
      )
    ])
    error_message = "Servers must be from the valid list: hgx-su00-h00, hgx-su00-h01, hgx-su01-h00, hgx-su01-h01."
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