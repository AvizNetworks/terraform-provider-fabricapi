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
  description = "SU index -> host count map, e.g. { \"0\" = \"1\" }. Optional: leave empty to have the provider derive it from hosts_per_su (both fields carry the same data)."
  type        = map(string)
  default     = {}
}

variable "starting_subnet_gpu" {
  description = "Starting GPU subnet, e.g. \"192\"."
  type        = string
}

variable "enable_ew" {
  description = "Enable east-west networking."
  type        = bool
  default     = true
}

variable "hosts_per_su" {
  description = "Raw suHostCnt value expected by the API, e.g. \"{0:1}\"."
  type        = string
}

variable "tenant_ctrl" {
  description = "Tenant context for the addFabricData call, e.g. \"ones\"."
  type        = string
  default     = "ones"
}

variable "enable_ns" {
  description = "Enable north-south (front-end user/storage) networking, matching the ONES UI's \"N-S (Front-End) Network\" section. When true, frontend_storage and starting_subnet_cpu are required."
  type        = bool
  default     = false
}

variable "dedicated_storage" {
  description = "Use a separate subnet for storage NICs instead of sharing the user/storage subnet. Only meaningful when enable_ns is true. Requires starting_subnet_storage."
  type        = bool
  default     = false
}

variable "frontend_storage" {
  description = "Use a separate subnet for front-end CPU NICs. Required (must be true) when enable_ns is true. Requires starting_subnet_cpu."
  type        = bool
  default     = false
}

variable "starting_subnet_cpu" {
  description = "Starting subnet for CPU NICs, e.g. \"10.2\". Required when enable_ns is true."
  type        = string
  default     = ""
}

variable "starting_subnet_storage" {
  description = "Starting subnet for storage NICs, e.g. \"10.3\". Required when dedicated_storage is true."
  type        = string
  default     = ""
}

variable "starting_subnet_tenants" {
  description = "Starting subnet for the tenant (user/storage) IP pool, e.g. \"10.4\" (the provider appends a trailing \".0\" octet before sending it to the API). Only meaningful when enable_ns is true."
  type        = string
  default     = ""
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

variable "custom_yaml_path" {
  description = <<-EOT
    Optional path to a fabric YAML to deploy instead of the server's own generated YAML.
    Only used when var.deploy is true. Two ways to get here:
      - Scenario 3 (design -> review -> edit -> deploy): first apply with deploy=false,
        run `terraform output -raw generated_yaml > fabric.review.yaml`, hand-edit that
        file, then re-apply with deploy=true and custom_yaml_path="fabric.review.yaml".
      - Scenario 4 (deploy a YAML from elsewhere): point this at an existing YAML file and
        set deploy=true on the very first apply — no review step needed.
    Leave unset (default) for the ordinary flow: deploy the server's current generated YAML
    as-is (Scenario 1).
  EOT
  type    = string
  default = ""
}

