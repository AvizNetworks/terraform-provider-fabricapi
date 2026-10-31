variable "fabric_name" {
  description = "Fabric to read. Leave empty to use provider fabric (FABRIC_NAME)."
  type        = string
  default     = ""
}

variable "rail_name" {
  description = "Optional single rail (e.g. r0, or p0-r0 on multi-plane fabrics)."
  type        = string
  default     = ""
}
