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
  - VPC peering name + fabric

## Local usage (no Docker)

Run Terraform directly from your host, using `-chdir=...` to select the example root you want.

## 01 - Create tenant(s)

This root uses **`for_each`** so you can manage **multiple tenants in one state** (each map key is one `fabricapi_tenant`). That avoids Terraform replacing tenant A when you add tenant B: the old behaviour came from a **single** resource whose `tenant_name` had `RequiresReplace()` when changed.

- **Multi-tenant (recommended):** set `var.tenants` (see `terraform.tfvars.example`).
- **Legacy single-tenant:** leave `tenants` empty and pass `tenant_name`, `tenant_description`, `max_gpus_allowed`, etc.

Default is **`respond-sync`** (HTTP `Prefer` header). You may use **`respond-async`** for tenant and tenant-servers: the provider polls **`GET /operations/{id}`** until the job finishes whenever the API returns an operation id (including with webhooks enabled). **`fabricapi_vpcpeering`** remains **sync-only** (backend does not support async there). To force sync-only without rebuilding, set environment variable **`FABRICAPI_DISABLE_ASYNC=1`** before running Terraform.

Requires **Terraform >= 1.5** (for `check` blocks).

```bash
export FABRIC_API_ENDPOINT="http://localhost:8787"
export FABRIC_NAME="1SU-Fabric170619"

# If you rebuilt the local provider binary, Terraform may reject it due to
# outdated checksums in `.terraform.lock.hcl`. If that happens, delete the
# lock file(s) in each example root and re-run init.

rm -f examples/decoupled/01-tenant/.terraform.lock.hcl
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

### Async + webhooks

For **`respond-async`**, set **`webhooks_enabled=true`** only when you also set a reachable **`webhook_url`** and non-empty **`webhook_events`** (API requirement). The provider still waits on the operation id before completing apply. Full async e2e command lists are in **`README.make.md`** and **`README.docker.md`**.

### Non-interactive runs (avoid runtime prompts)

Terraform will prompt at runtime if any **required** input variables are missing. To ensure a fully non-interactive run, always pass required values via `-var` (or use a `*.tfvars` file).

Example (non-interactive run):

```bash
terraform -chdir=examples/decoupled/01-tenant apply -auto-approve \
  -var="tenant_name=terraform_test1" \
  -var="tenant_description=terraform_test tenant" \
  -var="max_gpus_allowed=8"
```

## FM device license (testbed / lab — avoids HTTP 403 on GPU allocate)

Operations such **`fabricapi_tenant_servers`** call FM endpoints that enforce **`checkFabricDevicesLicense`**. If Quartz or RA sync has set devices to **DENIED**, FM returns **403** with a body like *"not in LICENSED state"* even though tenant create succeeded.

**Terraform does not fix this** — refresh FM’s Postgres `device_license` table **immediately before** `02-servers` / `03-vpcpeering` apply (and again if a long `apply` / async poll crosses a license refresh window).

From the **terraform-provider-fabricapi** repo root:

```bash
chmod +x examples/decoupled/scripts/refresh-fm-device-licenses.sh
# If your user cannot use docker.sock (e.g. aviz): prefix docker with sudo
DOCKER="sudo docker" ./examples/decoupled/scripts/refresh-fm-device-licenses.sh
```

Edit **`DEVICE_IPS`** inside the script (or export `DEVICE_IPS` and extend the script if you prefer) to match the IPs FM lists in the 403 error for your fabric.

**One-liner** equivalent (same defaults as the script):

```bash
sudo docker exec -i ones-fm-db psql -U postgres -d ones_fm -c "
UPDATE public.device_license
   SET license_state='LICENSED', features='FM', updated_at=NOW()
 WHERE device_ip IN (
   '192.168.122.8','192.168.122.9','192.168.122.10','192.168.122.11',
   '192.168.122.12','192.168.122.13','192.168.122.14','192.168.122.15','192.168.122.16');"
```

Then run **`terraform apply`** for `02-servers` right away.

## 02 - Allocate GPUs (servers)

```bash
# Lab: refresh device licenses first (see section above), then:
rm -f examples/decoupled/02-servers/.terraform.lock.hcl
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
rm -f examples/decoupled/03-vpcpeering/.terraform.lock.hcl
terraform -chdir=examples/decoupled/03-vpcpeering init
terraform -chdir=examples/decoupled/03-vpcpeering apply -auto-approve \
  -var="tenant_name=madhu01" \
  -var="fabric=1SU-Fabric170619" \
  -var="vpcpeering_name=tf-vpcpeering-madhu01" \
  -var="delete_on_destroy=false"
```

## Troubleshooting (lab)

### Webhook delivery fails with `PKIX path building failed` (logs show WARN, allocation still SUCCESS)

FM posts the webhook to your `webhook_url` using **Java’s default trust store**. If the URL is **`https://...`** and the server uses a **self-signed or private CA** certificate (common on `10.4.5.132`), delivery fails with PKIX. **GPU allocation itself can still succeed** — the failure is **notification only**; see `AsyncGpuAllocationService ... status=SUCCESS` in FM logs.

**Mitigations for Terraform e2e:**

- Turn off webhooks for the run: `-var="webhooks_enabled=false"` (omit `webhook_url` / `webhook_events` if your provider requires that when disabled), **or**
- Use an **`http://`** webhook URL to a listener that does not require TLS, **or**
- Install the FM-facing CA/cert into the FM JVM truststore (ops change).

### UI shows “0/8” on Tenant Allocation but tenant list shows “8 / 8” (or a “deallocation” toast)

Treat **FM logs + Terraform apply result** as the first source of truth. Your logs show `Updated tenant tenw01 gpusAllocated to 8 [AllotedGpus: hgx-su00-h00]` and **async SUCCESS** — that matches **8 GPUs allocated** for that host.

If the **per-node** screen still shows **0/8**:

- Hard **refresh** the browser (or reopen **Manage → Fabrics → Tenants →** `tenw01` **→ Tenant Allocation**).
- Confirm you are viewing **the same tenant** (`tenw01`) and **the same node** (`hgx-su00-h00`) that Terraform passed in `servers`.
- The **tenant list** column aggregates **gpusAllocated**; the **allocation** page may load from another query that lagged or cached; a full navigation refresh usually reconciles.

A **“deallocation done”** style message can also come from **other UI actions** or **stale notifications**; correlate the **timestamp** with FM logs for that window.

### Quartz `LICENSED → DENIED` during a long async allocate

You may see license transitions **mid-flight**. The **PATCH** that starts the job already passed `checkFabricDevicesLicense` after your `refresh` script; later **webhook / backupConfig** calls can still hit **DENIED** if Quartz reverted rows. That does not necessarily undo an allocation that already **completed SUCCESS** in the same operation.

## Notes

- GPU deallocation is step 02 with `operation=DELETE` (or `REMOVE`):
  - `shared` is optional and ignored for DELETE; you can omit it:
    `terraform -chdir=examples/decoupled/02-servers apply -auto-approve ... -var="operation=DELETE"`
- Tenant deletion:
  - `terraform -chdir=examples/decoupled/01-tenant destroy -auto-approve -var="tenant_name=..."`

