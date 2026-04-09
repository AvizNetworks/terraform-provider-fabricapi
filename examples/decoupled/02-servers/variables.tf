variable "tenant_fabric" {
  description = "Fabric name for GPU allocation endpoint /fabrics/{fabric}/tenants/{tenant}."
  type        = string
}

variable "tenant_name" {
  description = "Tenant name for GPU allocation/deallocation."
  type        = string
}

variable "servers" {
  description = "List of servers to allocate GPUs from."
  type        = list(string)
}

variable "operation" {
  description = "Operation for server management: ADD, DELETE, or REMOVE (REMOVE is alias for DELETE)"
  type        = string
}

variable "shared" {
  description = "Optional shared flag in PATCH payload."
  type        = bool
}

