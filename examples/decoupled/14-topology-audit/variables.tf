variable "fabric_name" {
  description = "Fabric to audit. Leave empty to use provider fabric (FABRIC_NAME)."
  type        = string
  default     = ""
}

variable "run_trigger" {
  description = "Arbitrary value; change it (e.g. a timestamp) to run the audit again. Required: without a change here, re-applying never re-runs an already-created audit."
  type        = string
  default     = "1"
}

variable "timeout" {
  description = "How long to wait for the audit job to finish, as a Go duration string."
  type        = string
  default     = "10m"
}

variable "fail_on_non_pass" {
  description = "If true, a FAIL or INCOMPLETE outcome fails `terraform apply` instead of succeeding with a warning."
  type        = bool
  default     = false
}
