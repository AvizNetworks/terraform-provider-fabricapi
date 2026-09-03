variable "name" {
  description = "Fabric name."
  type        = string
}

variable "type" {
  description = "Fabric type, e.g. \"Aviz RA\"."
  type        = string
  default     = "Aviz RA"
}

variable "description" {
  description = "Fabric description."
  type        = string
  default     = ""
}

variable "num_of_sus" {
  description = "Number of SUs."
  type        = number
}

variable "max_num_of_sus" {
  description = "Maximum number of SUs."
  type        = number
}

variable "host_map" {
  description = "SU index -> host count map, e.g. { \"0\" = \"1\" }."
  type        = map(string)
}

variable "starting_subnet_gpu" {
  description = "Starting GPU subnet, e.g. \"192\"."
  type        = string
}

variable "simulation_id" {
  description = "Simulation id."
  type        = number
}

variable "enable_ew" {
  description = "Enable east-west networking."
  type        = bool
  default     = true
}

variable "su_host_cnt" {
  description = "Raw suHostCnt value expected by the API, e.g. \"{0:1}\"."
  type        = string
}

variable "tenant" {
  description = "Tenant context for the addFabricData call, e.g. \"ones\"."
  type        = string
  default     = "ones"
}

variable "deploy" {
  description = "If true, also push the generated config to real switches and mark the fabric Deployed (fabricapi_fabric_deploy). If false, only design/review the fabric (Draft)."
  type        = bool
  default     = false
}

variable "deployment_type" {
  description = "One of DEFAULT, PARTIAL_CONFIG, or BROWNFIELD. Only used when var.deploy is true."
  type        = string
  default     = "DEFAULT"
}

variable "devices" {
  description = <<-EOT
    Every switch/server/DPU in the fabric, with real connection credentials. Only used when
    var.deploy is true and var.devices_file is unset. Must cover every device from the
    fabric's generated inventory (check the addFabricData response, or the ONES UI's Devices
    tab, for hostnames/roles).
  EOT
  type = list(object({
    hostname     = string
    ip           = string
    username     = string
    password     = string
    device_type  = optional(string)
    device_role  = optional(string)
    apply_config = optional(bool, true)
  }))
  default   = []
  sensitive = true
}

variable "devices_file" {
  description = <<-EOT
    Optional path to a JSON file holding the same shape as var.devices (a JSON array of
    objects with hostname/ip/username/password/device_role/...). When set, this takes
    precedence over var.devices — handy when the credential list already exists as a file
    (e.g. exported from a lab topology tool) instead of being hand-written as HCL. Keep this
    file out of version control (see devices.json.example for the format).
  EOT
  type    = string
  default = ""
}

