variable "tenant_name" {
  description = "Tenant name for VPC peering."
  type        = string
}

variable "vpcpeering_name" {
  description = "Name for the VPC peering object."
  type        = string
}

variable "delete_on_destroy" {
  description = "If true, attempt to delete vpcpeering on destroy (provider warns; delete not implemented)."
  type        = bool
  default     = false
}
