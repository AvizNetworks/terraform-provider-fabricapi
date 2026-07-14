# --- Multi-tenant (recommended): one fabricapi_tenant per map key ---
variable "tenants" {
  description = <<-EOT
    Map of tenant name => settings. Each key creates a separate fabricapi_tenant (separate state address),
    so multiple tenants coexist. When this map is non-empty, legacy flat variables below are ignored.
  EOT

  type = map(object({
    description      = string
    max_gpus_allowed = optional(number, 8)
    prefer           = optional(string, "respond-sync")
    webhooks_enabled = optional(bool, false)
    webhook_url      = optional(string)
    webhook_events   = optional(list(string))
  }))

  default = {}
}

# --- Legacy single-tenant (used only when var.tenants is empty) ---
variable "tenant_name" {
  description = "Legacy: tenant name. Ignored when var.tenants is non-empty."
  type        = string
  default     = ""
}

variable "tenant_description" {
  description = "Legacy: description. Optional; if omitted, the module generates a non-empty default description."
  type        = string
  default     = ""
}

variable "max_gpus_allowed" {
  description = "Legacy: max GPUs (8/16/24/32). Ignored when var.tenants is non-empty."
  type        = number
  default     = 8
}

variable "prefer" {
  description = "Legacy: HTTP Prefer value (respond-sync default, or respond-async). Ignored when var.tenants is non-empty."
  type        = string
  default     = "respond-sync"
}

variable "webhooks_enabled" {
  description = "Legacy: ignored when var.tenants is non-empty."
  type        = bool
  default     = false
}

variable "webhook_url" {
  description = "Legacy: ignored when var.tenants is non-empty."
  type        = string
  default     = "http://localhost:8787/test/webhook-receiver"
}

variable "webhook_events" {
  description = "Legacy: ignored when var.tenants is non-empty."
  type        = list(string)
  default     = []
}