variable "fabric_name" {
  description = "Fabric with IPAM in DHCP mode. Leave empty to use provider fabric (FABRIC_NAME)."
  type        = string
  default     = ""
}

variable "tenant_name" {
  description = "Tenant whose DHCP subnet to read (and retry)."
  type        = string
}

variable "reconcile_trigger" {
  description = "Set (and change) to run a fabric DHCP reconcile."
  type        = string
  default     = ""
}

variable "retry_trigger" {
  description = "Set (and change) to retry the tenant's DHCP provisioning."
  type        = string
  default     = ""
}
