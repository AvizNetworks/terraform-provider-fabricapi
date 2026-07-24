variable "fabric_name" {
  description = "Fabric for VF assign/unassign. Leave empty to use provider fabric (FABRIC_NAME)."
  type        = string
  default     = ""
}

variable "server_name" {
  description = "GPU server hostname that owns the VF (must already be attached to the tenant)."
  type        = string
}

variable "vf_id" {
  description = "VF path id — host server_if (e.g. vf4) or DPU if_name (e.g. pf1vf0_if)."
  type        = string
}

variable "tenant_name" {
  description = "Tenant to bind/unbind (sent as tenantName in POST and DELETE bodies)."
  type        = string
}

variable "prefer" {
  description = "Prefer mode: respond-sync (default). Async (respond-async) is disabled in the current release."
  type        = string
  default     = "respond-sync"

  validation {
    condition = !contains(
      ["respond-async", "respond_async"],
      lower(trimspace(var.prefer)),
    )
    error_message = "Async is currently not supported. Use prefer=respond-sync (default)."
  }
}
