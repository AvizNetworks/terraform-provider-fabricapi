# Decoupled workflow (3 separate applies)

This folder splits the original `examples/main.tf` into three independent Terraform roots so a user can run:

- tenant creation
- GPU allocation (tenant servers)
- VPC peering

These examples are designed for a strict “user supplies inputs” workflow:

- Set **only connection settings once** via environment variables:
  - `FABRIC_API_ENDPOINT`
  - `FABRIC_NAME`
- For every operation, the user passes required inputs explicitly via `-var`:
  - tenant name / description / max GPUs
  - servers + shared + operation
  - VPC peering name + target fabric + tenant fabric

## 01 - Create tenant

```bash
export FABRIC_API_ENDPOINT="http://localhost:8787"
export FABRIC_NAME="1SU-Fabric170619"

terraform -chdir=examples/decoupled/01-tenant init
terraform -chdir=examples/decoupled/01-tenant apply -auto-approve \
  -var="tenant_name=madhu01" \
  -var="tenant_description=TF Test tenant for GPU workloads" \
  -var="max_gpus_allowed=32"
```

## 02 - Allocate GPUs (servers)

```bash
terraform -chdir=examples/decoupled/02-servers init
terraform -chdir=examples/decoupled/02-servers apply -auto-approve \
  -var="tenant_fabric=1SU-Fabric170619" \
  -var="tenant_name=madhu01" \
  -var='servers=["hgx-su00-h00","hgx-su00-h01","hgx-su00-h02","hgx-su00-h03"]' \
  -var="shared=true" \
  -var="operation=ADD"
```

## 03 - Create VPC peering

```bash
terraform -chdir=examples/decoupled/03-vpcpeering init
terraform -chdir=examples/decoupled/03-vpcpeering apply -auto-approve \
  -var="tenant_fabric=1SU-Fabric170619" \
  -var="tenant_name=madhu01" \
  -var="target_fabric=1SU-Fabric170619" \
  -var="vpcpeering_name=tf-vpcpeering-madhu01" \
  -var="delete_on_destroy=false"
```

## Notes

- GPU deallocation is step 02 with `operation=DELETE` (or `REMOVE`):
  - `terraform -chdir=examples/decoupled/02-servers apply -auto-approve ... -var="operation=DELETE"`
- Tenant deletion:
  - `terraform -chdir=examples/decoupled/01-tenant destroy -auto-approve -var="tenant_name=..." -var="tenant_description=..." -var="max_gpus_allowed=..."`

