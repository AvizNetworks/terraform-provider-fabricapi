variable "api_endpoint" {
  description = "API endpoint URL"
  type        = string
  default     = "http://worker07.air.nvidia.com:29123"
}

variable "fabric_name" {
  description = "Fabric name"
  type        = string
  default     = "fab"
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
  description = "Maximum number of GPUs allowed for the tenant"
  type        = number
  default     = 16
  validation {
    condition     = var.max_gpus_allowed > 0
    error_message = "Max GPUs must be greater than 0."
  }
}

variable "servers" {
  description = "List of servers to add to the tenant"
  type        = list(string)
  default     = []
}

variable "operation" {
  description = "Operation for server management: ADD or REMOVE"
  type        = string
  default     = "ADD"
  validation {
    condition     = contains(["ADD", "REMOVE"], var.operation)
    error_message = "Operation must be either ADD or REMOVE."
  }
}