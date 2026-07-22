variable "fabric_name" {
  description = "Fabric to query for available servers. Leave empty to use provider fabric (FABRIC_NAME)."
  type        = string
  default     = ""
}
