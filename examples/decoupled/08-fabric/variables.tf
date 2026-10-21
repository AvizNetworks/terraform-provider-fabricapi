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
        derive it from hosts_per_su (both fields carry the same data). Ignored when
        rack_host_map is set.
      - rack_host_map: gb200/gb300 only. SU index -> {rack name -> host count}, e.g.
        { "0" = { "rack1" = "5", "rack2" = "6", "rack3" = "4" } } for a gb300 SU with 3 racks
        (gb200 uses 2). Required for gb200/gb300: a flat host_map/hosts_per_su count is rejected,
        since ONES would apply it to every rack and multiply the hosts.
        hosts_per_su is still required alongside this and must carry the same data as raw JSON
        (e.g. "{\"0\":{\"rack1\":\"5\",\"rack2\":\"6\",\"rack3\":\"4\"}}"), matching how the
        ONES UI round-trips suHostCnt for gb200/gb300.
      - tenant_ctrl: tenant context for the addFabricData call, e.g. "ones". Unrelated to the
        ONES UI's ONES/External "Tenant control" radio — that is tenant_control.
      - tenant_control: the ONES UI's "Tenant control" radio, "ones" (default) or "external".
        With "external", network_config.starting_subnet_tenants is required when N-S is on.
      - simulation_id: raw simulationId value expected by the API. Defaults to 1 if unset.
  EOT
  type = object({
    node_type         = optional(string)
    number_of_sus     = number
    max_number_of_sus = number
    hosts_per_su      = string
    host_map          = optional(map(string))
    rack_host_map     = optional(map(map(string)))
    tenant_ctrl       = optional(string, "ones")
    tenant_control    = optional(string)
    simulation_id     = optional(number)
  })
}

variable "network_config" {
  description = <<-EOT
    East-west and north-south networking configuration.
      - enable_east_west_networking / starting_subnet_gpu: east-west (GPU) networking.
      - network_type: E-W technology, "ethernet" (default; gb200 defaults to "infiniband") or
        "infiniband". InfiniBand takes no starting_subnet_gpu. SpX RA 2.1 is Ethernet only,
        gb200 is InfiniBand only. The provider derives the fabric's gpuScaleMode from this.
      - ew_tenant_aware: the E-W "Tenant aware" Yes/No (Ethernet only). Defaults to false.
      - starting_subnet_tenants: tenant compute IP pool first octet ("10", "172" or "192"),
        only with su_config.tenant_control = "external".
      - enable_north_south_networking: front-end user/storage networking, matching the ONES
        UI's "N-S (Front-End) Network" section. When true, starting_subnet_cpu is required.
      - ns_operating_system: "cumulus" (default) or "sonic" for the north-south switches,
        matching the ONES UI's OS selector in that section. Must match the real devices'
        actual OS or switch validation fails during deploy. East-west is always Cumulus.
      - dedicated_storage / starting_subnet_storage: optional pair (required together) that
        splits storage NICs onto their own subnet instead of sharing the CPU one — the ONES
        UI's "Dedicated Storage Network" switch.
      - dpu_hbn: enable DPU/HBN offload for the east-west network — GPU hosts connect through
        a BlueField DPU running HBN instead of directly to the E-W leaf, matching the ONES UI's
        HBN toggle. Only meaningful when enable_east_west_networking is true. Devices on an HBN
        fabric are managed with fabricapi_vf_interfaces/fabricapi_vf_assign (see
        examples/decoupled/06-vf-interfaces and 07-vf-assign), not the server/GPU allocation
        resources.
      - is_vxlan_fabric: tenant segmentation — true for VXLAN (overlay), false for VLAN, matching
        the ONES UI's "Tenant Segmentation" radio. Fabric-wide, applied to every tenant's
        generated config, not a per-tenant setting. Defaults to false (VLAN) if unset, like the
        UI. Forced to VXLAN when dpu_hbn is true, or for SONiC N-S on fabric types other than
        Aviz RA 1.0 (left unset here so the provider can apply that).
      - storage_tenant_aware: whether storage NICs participate in tenant isolation, matching the
        ONES UI's "Tenant aware (Storage)" Yes/No toggle. Defaults to false if unset (forced to
        true for SONiC N-S on fabric types other than Aviz RA 1.0).
  EOT
  type = object({
    enable_east_west_networking   = optional(bool, true)
    network_type                  = optional(string)
    starting_subnet_gpu           = optional(string)
    ew_tenant_aware               = optional(bool)
    starting_subnet_tenants       = optional(string)
    enable_north_south_networking = optional(bool, false)
    ns_operating_system           = optional(string)
    dedicated_storage             = optional(bool, false)
    starting_subnet_cpu           = optional(string)
    starting_subnet_storage       = optional(string)
    dpu_hbn                       = optional(bool, false)
    is_vxlan_fabric               = optional(bool)
    storage_tenant_aware          = optional(bool)
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

