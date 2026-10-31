variable "fabric_name" {
  description = "Fabric (External tenant control). Leave empty to use provider fabric (FABRIC_NAME)."
  type        = string
  default     = ""
}

variable "policy_name" {
  description = "Policy name, unique within the fabric, at most 24 characters."
  type        = string
}

variable "source_type" {
  description = "\"tenant\" (whole tenant) or \"service\" (endpoints)."
  type        = string
  default     = "tenant"
}

variable "source_tenant" {
  type = string
}

variable "source_endpoints" {
  description = "Source endpoints; only for source_type = \"service\"."
  type        = list(string)
  default     = []
}

variable "destination_type" {
  description = "\"tenant\" (whole-tenant route leak; source must be a tenant too) or \"service\"."
  type        = string
  default     = "service"
}

variable "destination_tenant" {
  type = string
}

variable "service_name" {
  description = "Optional service label."
  type        = string
  default     = ""
}

variable "destination_endpoints" {
  description = "Destination endpoints, e.g. [\"192.2.127.129/32\"]. Required for destination_type = \"service\"."
  type        = list(string)
  default     = []
}
