variable "name" {
  description = "Fabric name."
  type        = string
}

variable "type" {
  description = "Fabric type. One of \"NVIDIA SpX RA 1.3\", \"NVIDIA SpX RA 2.1\", \"Aviz RA 1.0\"."
  type        = string
  default     = "Aviz RA 1.0"
}

variable "description" {
  description = "Fabric description."
  type        = string
  default     = ""
}

variable "su_config" {
  description = <<-EOT
    Scale-unit and tenant configuration, mirroring the ONES UI's SU-config step.
      - node_type: GPU node hardware, e.g. "gb200", "gb300", "b300_32", "b300_64", "rtxpro_4",
        "rtxpro_8". Leave unset for the default ("dgx") — dgx is not itself a value you need to set.
      - number_of_sus / max_number_of_sus: scale-unit counts.
      - hosts_per_su: raw suHostCnt value expected by the API, e.g. "{0:1}".
      - host_map: SU index -> host count, e.g. { "0" = "1" }. Leave unset to have the provider
        derive it from hosts_per_su (both fields carry the same data).
      - tenant_ctrl: tenant context for the addFabricData call, e.g. "ones". Unrelated to the
        ONES UI's ONES/External "Tenant control" radio, which this provider doesn't expose.
      - simulation_id: raw simulationId value expected by the API. Defaults to 1 if unset.
  EOT
  type = object({
    node_type         = optional(string)
    number_of_sus     = number
    max_number_of_sus = number
    hosts_per_su      = string
    host_map          = optional(map(string))
    tenant_ctrl       = optional(string, "ones")
    simulation_id     = optional(number)
  })
}

variable "network_config" {
  description = <<-EOT
    East-west and north-south networking configuration.
      - enable_east_west_networking / starting_subnet_gpu: east-west (GPU) networking.
      - enable_north_south_networking: front-end user/storage networking, matching the ONES
        UI's "N-S (Front-End) Network" section. When true, starting_subnet_cpu is required.
      - ns_operating_system: "cumulus" (default) or "sonic" for the north-south switches,
        matching the ONES UI's OS selector in that section. Must match the real devices'
        actual OS or switch validation fails during deploy. East-west is always Cumulus.
      - dedicated_storage / starting_subnet_storage: optional pair (required together) that
        splits storage NICs onto their own subnet instead of sharing the CPU one — the ONES
        UI's "Dedicated Storage Network" switch.
  EOT
  type = object({
    enable_east_west_networking   = optional(bool, true)
    starting_subnet_gpu           = optional(string)
    enable_north_south_networking = optional(bool, false)
    ns_operating_system           = optional(string)
    dedicated_storage             = optional(bool, false)
    starting_subnet_cpu           = optional(string)
    starting_subnet_storage       = optional(string)
  })
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

