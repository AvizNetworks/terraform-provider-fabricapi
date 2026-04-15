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

variable "prefer" {
  description = "Prefer mode: respond-sync (default) or respond-async (HTTP Prefer). Underscore forms are accepted."
  type        = string
  default     = "respond-sync"
}

variable "webhooks_enabled" {
  description = "Enable webhook payload for async VPC peering create."
  type        = bool
  default     = false
}

variable "webhook_url" {
  description = "Webhook receiver URL (used only when prefer is respond-async and webhooks_enabled=true)."
  type        = string
  default     = "http://localhost:8787/test/webhook-receiver"
}

variable "webhook_events" {
  description = "Webhook events (used only when prefer is respond-async and webhooks_enabled=true)."
  type        = list(string)
  default     = []
}

