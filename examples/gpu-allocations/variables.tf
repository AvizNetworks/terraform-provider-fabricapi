variable "tenant_name" {
  description = "Name of the tenant to allocate GPUs for"
  type        = string
}

variable "operation" {
  description = "ADD to map GPUs, DELETE to unmap GPUs"
  type        = string
  default     = "ADD"
  validation {
    condition     = contains(["ADD", "DELETE"], var.operation)
    error_message = "operation must be ADD or DELETE."
  }
}

variable "servers" {
  description = <<-EOT
    List of server GPU entries to allocate/deallocate.
    Each entry requires:
      index    - server index string ("0", "1", ...)
      hostname - compute node hostname
      gpus     - list of GPU ids on that node (e.g. ["G0","G1","G2","G3"])
  EOT
  type = list(object({
    index    = string
    hostname = string
    gpus     = list(string)
  }))
}
