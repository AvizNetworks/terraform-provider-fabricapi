#!/usr/bin/env bash
# Single QA entrypoint for decoupled VPC peering (03-vpcpeering).
#
# Default: same as plain `terraform apply` (first time = create; later = "No changes" if state matches).
#
# After you recreate a tenant / GPUs but 03-vpcpeering state still shows an old peering, Terraform
# may report "No changes" and skip the API. Opt in to force a recreate (state drop + new Create):
#   export FABRICAPI_VPC_PEERING_FORCE_RECREATE=1
#
# Usage (from repo root):
#   export FABRIC_API_ENDPOINT="http://10.4.5.76:8787"
#   export FABRIC_NAME="8407"
#   ./scripts/apply-vpc-peering.sh
#
# Optional:
#   TENANT_NAME=tenant-003 VPC_PEERING_NAME=tenant-003-storage-peering ./scripts/apply-vpc-peering.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHDIR="${TF_CHDIR:-examples/decoupled/03-vpcpeering}"

FABRIC_API_ENDPOINT="${FABRIC_API_ENDPOINT:-http://localhost:8787}"
FABRIC_NAME="${FABRIC_NAME:-8407}"
TENANT_NAME="${TENANT_NAME:-tenant-003}"
# Optional override for examples/decoupled/03-vpcpeering `fabric`. If unset, Terraform uses provider fabric (FABRIC_NAME).
FABRIC_FOR_VPC="${FABRIC_FOR_VPC:-}"
VPC_PEERING_NAME="${VPC_PEERING_NAME:-tenant-003-storage-peering}"
DELETE_ON_DESTROY="${DELETE_ON_DESTROY:-false}"

run_tf() {
  (cd "${REPO_ROOT}" && FABRIC_API_ENDPOINT="${FABRIC_API_ENDPOINT}" FABRIC_NAME="${FABRIC_NAME}" terraform "$@")
}

echo "==> terraform init -upgrade ($CHDIR)"
run_tf -chdir="$CHDIR" init -upgrade

REPLACE=()
force="${FABRICAPI_VPC_PEERING_FORCE_RECREATE:-}"
if [[ "$force" == "1" || "$force" == "true" || "$force" == "yes" ]]; then
  if run_tf -chdir="$CHDIR" state show fabricapi_vpcpeering.this >/dev/null 2>&1; then
    echo "==> FABRICAPI_VPC_PEERING_FORCE_RECREATE: applying with -replace=fabricapi_vpcpeering.this"
    REPLACE=(-replace=fabricapi_vpcpeering.this)
  else
    echo "==> FABRICAPI_VPC_PEERING_FORCE_RECREATE set but no resource in state yet; plain create"
  fi
fi

echo "==> terraform apply -auto-approve"
TF_APPLY_ARGS=(
  -chdir="$CHDIR" apply -auto-approve
  "${REPLACE[@]}"
  -var="tenant_name=$TENANT_NAME"
  -var="vpcpeering_name=$VPC_PEERING_NAME"
  -var="delete_on_destroy=$DELETE_ON_DESTROY"
)
if [[ -n "$FABRIC_FOR_VPC" ]]; then
  TF_APPLY_ARGS+=(-var="fabric=$FABRIC_FOR_VPC")
fi
run_tf "${TF_APPLY_ARGS[@]}"
