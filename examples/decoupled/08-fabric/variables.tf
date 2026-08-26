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

