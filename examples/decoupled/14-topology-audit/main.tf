terraform {
  required_providers {
    fabricapi = {
      source  = "local/fabricapi"
      version = "1.0.0"
    }
  }
}

provider "fabricapi" {
  # Prefer environment variables:
  #   FABRIC_API_ENDPOINT  (Fabric Manager, e.g. https://<ones-host>:8443)
  #   FABRIC_API_AUTH_ENDPOINT, FABRIC_NAME
}

# Triggers an on-demand Topology Audit (LLDP-vs-intent cabling validation) for a
# fabric instead of waiting for the next scheduled Rule Engine run. Runs on
# create and whenever `run_trigger` changes (e.g. -var="run_trigger=$(date +%s)").
# Terraform refresh/plan never re-triggers it; destroy only removes it from state.
resource "fabricapi_topology_audit" "this" {
  fabric_name      = var.fabric_name != "" ? var.fabric_name : null
  triggers         = { run = var.run_trigger }
  timeout          = var.timeout
  fail_on_non_pass = var.fail_on_non_pass
}

output "operation_id" {
  value = fabricapi_topology_audit.this.operation_id
}

output "job_status" {
  value = fabricapi_topology_audit.this.job_status
}

# PASS, FAIL, or INCOMPLETE.
output "overall_status" {
  value = fabricapi_topology_audit.this.overall_status
}

output "executed_at" {
  value = fabricapi_topology_audit.this.executed_at
}

# JSON-encoded per-link detail (device/localInterface/remoteDevice/remoteInterface/
# observedRemoteDevice/observedRemoteInterface/status/message), or the job's error
# message when job_status is FAILED.
output "discrepancies" {
  value = fabricapi_topology_audit.this.discrepancies
}
