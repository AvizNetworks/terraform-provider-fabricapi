variable "tenant_name" {
  description = "Tenant name for VPC peering."
  type        = string
}

variable "fabric" {
  description = "Fabric for tenant lookup and VPC peering API. Leave empty to use provider fabric (FABRIC_NAME). One value covers both; split overrides are not modeled in this example."
  type        = string
  default     = ""
}

variable "vpcpeering_name" {
  description = "Name for the VPC peering object."
  type        = string
}

variable "delete_on_destroy" {
  description = "If true, attempt to delete vpcpeering on destroy (provider warns; delete not implemented)."
  type        = bool
}

