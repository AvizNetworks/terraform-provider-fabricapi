variable "fabric_name" {
  description = "Fabric for the fabric/tenant registrations. Leave empty to use provider fabric (FABRIC_NAME)."
  type        = string
  default     = ""
}

variable "callback_url" {
  description = "Global webhook URL (https; not a loopback address)."
  type        = string
}

variable "event_types" {
  description = "Global webhook events: tenant.create, tenant.delete, tenant.allocate, tenant.deallocate, tenant.vpcpeering, fabric.deploy, fabric.delete."
  type        = list(string)
  default     = ["fabric.deploy", "fabric.delete"]
}

variable "enabled" {
  type    = bool
  default = true
}

variable "filter_fabric_names" {
  description = "Only these fabrics (empty = all)."
  type        = list(string)
  default     = []
}

variable "filter_tenant_names" {
  description = "Only these tenants (empty = all)."
  type        = list(string)
  default     = []
}

variable "fabric_callback_url" {
  description = "Set to also register a fabric-scoped webhook."
  type        = string
  default     = ""
}

variable "tenant_name" {
  description = "Set with tenant_callback_url to register a tenant-scoped webhook."
  type        = string
  default     = ""
}

variable "tenant_callback_url" {
  type    = string
  default = ""
}
