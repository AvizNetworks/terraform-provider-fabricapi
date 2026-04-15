variable "tenant_fabric" {
  description = "Fabric name for GPU allocation endpoint /fabrics/{fabric}/tenants/{tenant}."
  type        = string
}

variable "tenant_name" {
  description = "Tenant name for GPU allocation/deallocation."
  type        = string
}

variable "servers" {
  description = "List of servers to allocate GPUs from."
  type        = list(string)
}

variable "operation" {
  description = "Operation for server management: ADD, DELETE, or REMOVE (REMOVE is alias for DELETE)"
  type        = string
}

variable "shared" {
  description = "Optional shared flag in PATCH payload."
  type        = bool
}

variable "prefer" {
  description = "Prefer mode: respond-sync (default) or respond-async (HTTP Prefer). Underscore forms are accepted."
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
  description = "Webhook events (used only when prefer is respond-async and webhooks_enabled=true)."
  type        = list(string)
  default     = []
}

