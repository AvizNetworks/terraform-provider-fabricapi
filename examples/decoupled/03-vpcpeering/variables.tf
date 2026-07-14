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

variable "prefer" {
  description = "HTTP Prefer: respond-sync (default) or respond-async. When async returns an operation id, the provider polls until completion."
  type        = string
  default     = "respond-sync"
}

variable "webhooks_enabled" {
  description = "Enable webhook payload for async operations."
  type        = bool
  default     = false
}

variable "webhook_url" {
  description = "Webhook receiver URL (used only when prefer is respond-async and webhooks_enabled=true)."
  type        = string
  default     = "http://localhost:8787/test/webhook-receiver"
}

variable "webhook_events" {
  description = "Webhook events (used only when prefer is respond-async and webhooks_enabled=true). Must match Fabric API event names."
  type        = list(string)
  default     = []
}
