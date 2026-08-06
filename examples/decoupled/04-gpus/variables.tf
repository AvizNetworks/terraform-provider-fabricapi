variable "tenant_fabric" {
  description = "Fabric for /fabrics/{fabric}/tenants/{tenant}/gpus. Leave empty to use provider fabric (FABRIC_NAME / provider \"fabric\")."
  type        = string
  default     = ""
}

variable "tenant_name" {
  description = "Tenant name for GPU port assignment. The tenant must already exist (see 01-tenant)."
  type        = string
}

variable "server_names" {
  description = "Server names to assign/remove GPU ports on (maps to API's serverNames)."
  type        = list(string)
}

variable "gpu_ids" {
  description = "Optional 1-based GPU port indices on server_names (maps to API's gpuIds). Leave empty ([]) to act on the whole server (all GPUs on server_names); set specific indices to act at the per-GPU level."
  type        = list(number)
  default     = []
}

variable "operation" {
  description = "Operation for GPU port assignment: ADD or DELETE (REMOVE is accepted as a client-side alias for DELETE; the backend GpuAction enum only has ADD/DELETE)."
  type        = string
}

variable "membership" {
  description = "UFM-only PKey partition membership: \"full\" or \"limited\". Leave empty for NMXC fabrics or to use the backend default."
  type        = string
  default     = ""
}
