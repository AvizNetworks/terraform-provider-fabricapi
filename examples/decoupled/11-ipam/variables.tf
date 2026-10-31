variable "fabric_name" {
  description = "Fabric (External tenant control). Leave empty to use provider fabric (FABRIC_NAME)."
  type        = string
  default     = ""
}

variable "mode" {
  description = "\"static\" or \"dhcp\" (DHCP needs a Kea server configured for the fabric)."
  type        = string
  default     = "static"
}

variable "enable_ip_assign" {
  description = "Static mode: push each address to its server over SSH."
  type        = bool
  default     = false
}

variable "lease_time_seconds" {
  description = "DHCP mode: lease time (1-2592000). null keeps the fabric's saved value."
  type        = number
  default     = null
}

variable "force" {
  description = "Send ?force=true (skip the DHCP reachability probe; disable unused pools on destroy)."
  type        = bool
  default     = false
}

variable "tenant_name" {
  description = "Optional: tenant whose pool/allocations to read."
  type        = string
  default     = ""
}

variable "server_name" {
  description = "Optional: compute server whose addresses to read."
  type        = string
  default     = ""
}
