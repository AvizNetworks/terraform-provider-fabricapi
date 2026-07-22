variable "tenant_fabric" {
  description = "Fabric for /fabrics/{fabric}/tenants/{tenant}/gpuAllocations. Leave empty to use provider fabric."
  type        = string
  default     = ""
}

variable "tenant_name" {
  description = "Tenant name for per-GPU allocation/deallocation."
  type        = string
}

variable "operation" {
  description = "Operation: ADD, DELETE, or REMOVE (REMOVE aliases DELETE)."
  type        = string
  default     = "ADD"
}

variable "allocations" {
  description = <<-EOT
    Per-server GPU lists. Example matching the FM curl:
      [{ suid = 0, server = "su00-node00", gpus = ["G6", "G7"] }]
    GPU count is fabric-defined (often up to 8: G0–G7).
  EOT
  type = list(object({
    suid   = number
    server = string
    gpus   = list(string)
  }))
}

variable "prefer" {
  description = "Prefer mode: respond-sync (default). Async (respond-async) is disabled in the current release."
  type        = string
  default     = "respond-sync"

  validation {
    condition = !contains(
      ["respond-async", "respond_async"],
      lower(trimspace(var.prefer)),
    )
    error_message = "Async is currently not supported. Use prefer=respond-sync (default)."
  }
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
