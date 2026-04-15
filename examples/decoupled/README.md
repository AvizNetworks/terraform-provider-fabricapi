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

## Docker (interactive shell, common `/workspace`)

The **`terraform-fabricapi:latest`** image uses **`/workspace`** as the working directory (contents of repo `examples/`). Do **not** pass `-w /workspace/decoupled/01-tenant` unless you want to lock yourself to one module; instead:

```bash
docker run -it --rm \
  -e FABRIC_API_ENDPOINT="https://10.4.5.132:8089" \
  -e FABRIC_NAME="External" \
  -e FABRIC_API_AUTH_ENDPOINT="https://10.4.5.132:8089" \
  -e FABRIC_API_USERNAME="superadmin" \
  -e FABRIC_API_PASSWORD='Admin@1234' \
  -e FABRICAPI_INSECURE_TLS=1 \
  terraform-fabricapi:latest
cd decoupled/01-tenant && terraform init && terraform apply ...
```

## 01 - Create tenant(s)

This root uses **`for_each`** so you can manage **multiple tenants in one state** (each map key is one `fabricapi_tenant`). That avoids Terraform replacing tenant A when you add tenant B: the old behaviour came from a **single** resource whose `tenant_name` had `RequiresReplace()` when changed.

- **Multi-tenant (recommended):** set `var.tenants` (see `terraform.tfvars.example`).
- **Legacy single-tenant:** leave `tenants` empty and pass `tenant_name`, `tenant_description`, `max_gpus_allowed`, etc.

Use **`respond-sync`** / **`respond-async`** in Terraform (HTTP `Prefer` header). Underscore forms are still accepted by the provider.

Requires **Terraform >= 1.5** (for `check` blocks).

```bash
export FABRIC_API_ENDPOINT="http://localhost:8787"
export FABRIC_NAME="1SU-Fabric170619"

terraform -chdir=examples/decoupled/01-tenant init
terraform -chdir=examples/decoupled/01-tenant apply -auto-approve \
  -var="tenant_name=madhu01" \
  -var="tenant_description=TF Test tenant for GPU workloads" \
  -var="max_gpus_allowed=32"
```

Example: three tenants (sync, async without webhooks, async with webhooks) via tfvars:

```bash
cp examples/decoupled/01-tenant/terraform.tfvars.example examples/decoupled/01-tenant/terraform.tfvars
# edit URLs, events, and credentials as needed
terraform -chdir=examples/decoupled/01-tenant apply -auto-approve
```

### Migrating existing state (single resource → for_each)

If you previously had `fabricapi_tenant.this` with no index and upgrade to this layout:

```bash
terraform -chdir=examples/decoupled/01-tenant state mv \
  'fabricapi_tenant.this' \
  'fabricapi_tenant.this["madhu01"]'
```

Use the **actual** tenant name in place of `madhu01`.

### Optional async + webhooks (tenant + servers + VPC peering)

- Tenant create/delete, GPU allocation/deallocation, and VPC peering create support async execution via `prefer` and optional webhooks (same rules as the main README).

Example: async + webhooks (tenant create, legacy vars):

```bash
terraform -chdir=examples/decoupled/01-tenant apply -auto-approve \
  -var="tenant_name=madhu01" \
  -var="tenant_description=TF Test tenant for GPU workloads" \
  -var="max_gpus_allowed=32" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var="webhook_url=http://localhost:8787/test/webhook-receiver" \
  -var='webhook_events=["tenant.create"]'
```

### Non-interactive runs (avoid runtime prompts)

Terraform will prompt at runtime if any **required** input variables are missing. To ensure a fully non-interactive run, always pass required values via `-var` (or use a `*.tfvars` file).

Example **inside the default `terraform-fabricapi` image** (examples live under **`/workspace`**, not `/repo`):

```bash
terraform -chdir=/workspace/decoupled/01-tenant apply -auto-approve \
  -var="tenant_name=terraform_test1" \
  -var="tenant_description=terraform_test tenant" \
  -var="max_gpus_allowed=8"
```

If you instead mount the repo at **`/repo`** (`docker run ... -v "$PWD:/repo" ...`), use **`/repo/examples/decoupled/01-tenant`** with `-chdir`.

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
  - Legacy: `terraform -chdir=examples/decoupled/01-tenant destroy -auto-approve -var="tenant_name=..." -var="tenant_description=..." -var="max_gpus_allowed=..."`
  - Multi-tenant: use the same `terraform.tfvars` (or `-var-file=...`) you used for apply so all instances in `for_each` are destroyed, or remove keys from `tenants` and `apply` to drop individual tenants.

