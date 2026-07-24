variable "fabric_name" {
  description = "Fabric to query for VF interfaces. Leave empty to use provider fabric (FABRIC_NAME)."
  type        = string
  default     = ""
}

variable "server_name" {
  description = "GPU server hostname whose DPU VF interfaces should be listed."
  type        = string
}
