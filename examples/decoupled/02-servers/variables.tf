variable "tenant_fabric" {
  description = "Fabric for /fabrics/{fabric}/tenants/{tenant}. Leave empty to use provider fabric (FABRIC_NAME / provider \"fabric\")."
  type        = string
  default     = ""
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
  description = "Webhook events (used only when prefer is respond-async and webhooks_enabled=true)."
  type        = list(string)
  default     = []
}

