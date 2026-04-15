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

