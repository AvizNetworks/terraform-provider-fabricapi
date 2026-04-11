variable "tenant_name" {
  description = "Name of the tenant to create"
  type        = string
}

variable "tenant_description" {
  description = "Description of the tenant"
  type        = string
  # Default exists so `terraform destroy` doesn't require passing it again.
  # Provider enforces it as required during Create.
  default     = ""
}

variable "max_gpus_allowed" {
  description = "Maximum number of GPUs allowed for the tenant (8/16/24/32)."
  type        = number
  default     = 8
}

