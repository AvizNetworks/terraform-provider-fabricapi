variable "tenant_name" {
  description = "Tenant name for VPC peering."
  type        = string
}

variable "tenant_fabric" {
  description = "Fabric where tenant lives (used to resolve tenant.vnets + defaultStorageName)."
  type        = string
}

variable "target_fabric" {
  description = "Fabric used in the VPC peering API endpoint: /fabrics/{target_fabric}/vpcpeering"
  type        = string
}

variable "vpcpeering_name" {
  description = "Name for the VPC peering object."
  type        = string
}

variable "delete_on_destroy" {
  description = "If true, attempt to delete vpcpeering on destroy (provider warns; delete not implemented)."
  type        = bool
}

