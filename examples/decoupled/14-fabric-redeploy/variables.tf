variable "fabric_name" {
  description = "Name of an already-Deployed fabric to redeploy. The only thing this root needs — no devices, no custom_yaml, no su_config/network_config: redeploy re-pushes whatever's already stored server-side from the last real deploy (via fabricapi_fabric_deploy)."
  type        = string
}

variable "allow_unreachable" {
  description = "Proceed even if the precheck finds devices that are unreachable (but not an outright login failure) — matches the ONES UI's \"Re-Deploy Anyway\". Never bypasses a real credential failure."
  type        = bool
  default     = false
}
