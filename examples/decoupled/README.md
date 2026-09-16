# Decoupled workflow

This folder splits operations into independent Terraform roots so you can run each step separately (tenant, servers, per-GPU, discovery, VPC peering).

Set **connection settings once** via environment variables:

- `FABRIC_API_ENDPOINT`
- `FABRIC_NAME`
- Auth: `FABRIC_API_USERNAME` / `FABRIC_API_PASSWORD`, or `FABRIC_API_ACCESS_TOKEN`

For each operation, pass inputs via `-var` (or a `*.tfvars` file).

## Recommended order (full tenant workflow)

| Step | Root | Terraform type | API | Notes |
|------|------|----------------|-----|-------|
| 1 | `01-tenant` | Resource | POST/DELETE tenant | Create tenant first |
| 2 | `05-available-servers` | **Data source** | GET `.../available_servers` | Optional; lists free hostnames |
| 3 | `02-servers` | Resource | PATCH tenant (ADD/DELETE servers) | Attach whole server(s) to tenant |
| 4 | `04-gpu-allocations` | Resource | POST `.../gpuAllocations` | Per-GPU slices (e.g. G6, G7) on attached servers |
| 4b | `04-gpus` | Resource | POST `.../tenants/{tenant}/gpus` | External UFM/NMX-C GPU-port assign (whole-server or `gpu_ids`); use when the server was attached with `shared=true` |
| 5 | `06-vf-interfaces` | **Data source** | GET `.../vf-interfaces` | HBN only; list free/provisioned VFs |
| 6 | `07-vf-assign` | Resource | POST/DELETE `.../vf-interfaces/{vfId}/assign` | HBN only; bind/unbind VF to tenant VLAN |
| 7 | `03-vpcpeering` | Resource | POST vpcpeering | Optional; after tenant networking is ready |

**Deallocate / cleanup (reverse order where applicable):**

- VF: `07-vf-assign` `destroy` (DELETE assign with `tenantName` body)
- Per-GPU: `04-gpu-allocations` with `operation=DELETE`
- External GPU ports: `04-gpus` with `operation=DELETE`
- Whole server: `02-servers` with `operation=DELETE`
- Tenant: `01-tenant` `destroy`

## State files

Each root keeps **its own** state file. Reuse the same `-state=...` path when continuing the same tenant workflow.

| Root | Typical state file | Lifecycle |
|------|-------------------|-----------|
| `01-tenant` | `states/e2e_tenant.tfstate` | Resource — destroy to delete tenant |
| `02-servers` | `states/e2e_servers.tfstate` | Resource — `operation=DELETE` to deallocate |
| `04-gpu-allocations` | `states/e2e_gpu_alloc.tfstate` | Resource — `operation=DELETE` to deallocate GPUs |
| `04-gpus` | `states/e2e_tenant_gpus.tfstate` | Resource — `operation=DELETE` to release GPU ports |
| `05-available-servers` | `states/e2e_available_servers.tfstate` | **Data source only** — refreshed each apply; no destroy needed |
| `06-vf-interfaces` | `states/e2e_vf_interfaces.tfstate` | **Data source only** — refreshed each apply; no destroy needed |
| `07-vf-assign` | `states/e2e_vf_assign.tfstate` | Resource — destroy to unbind VF |
| `03-vpcpeering` | `states/e2e_vpc.tfstate` | Resource — see VPC peering notes in Docker/Make README |

Use a **new state filename** when starting a different tenant or a clean run.

## One-time setup (local)

```bash
export FABRIC_API_ENDPOINT="https://10.4.5.132:8089"
export FABRIC_NAME="Get_fab"
export FABRIC_API_USERNAME="superadmin"
export FABRIC_API_PASSWORD="YOUR_PASSWORD"
export FABRICAPI_INSECURE_TLS=1

make install   # or ./docker-build.sh for Docker workflow

mkdir -p examples/decoupled/01-tenant/states
mkdir -p examples/decoupled/02-servers/states
mkdir -p examples/decoupled/03-vpcpeering/states
mkdir -p examples/decoupled/04-gpu-allocations/states
mkdir -p examples/decoupled/04-gpus/states
mkdir -p examples/decoupled/05-available-servers/states
mkdir -p examples/decoupled/06-vf-interfaces/states
mkdir -p examples/decoupled/07-vf-assign/states

rm -f examples/decoupled/01-tenant/.terraform.lock.hcl
rm -f examples/decoupled/02-servers/.terraform.lock.hcl
rm -f examples/decoupled/03-vpcpeering/.terraform.lock.hcl
rm -f examples/decoupled/04-gpu-allocations/.terraform.lock.hcl
rm -f examples/decoupled/04-gpus/.terraform.lock.hcl
rm -f examples/decoupled/05-available-servers/.terraform.lock.hcl
rm -f examples/decoupled/06-vf-interfaces/.terraform.lock.hcl
rm -f examples/decoupled/07-vf-assign/.terraform.lock.hcl

terraform -chdir=examples/decoupled/01-tenant init -upgrade
terraform -chdir=examples/decoupled/02-servers init -upgrade
terraform -chdir=examples/decoupled/03-vpcpeering init -upgrade
terraform -chdir=examples/decoupled/04-gpu-allocations init -upgrade
terraform -chdir=examples/decoupled/04-gpus init -upgrade
terraform -chdir=examples/decoupled/05-available-servers init -upgrade
terraform -chdir=examples/decoupled/06-vf-interfaces init -upgrade
terraform -chdir=examples/decoupled/07-vf-assign init -upgrade
```

**Fabric name is case-sensitive** — use the exact name from `GET /fabrics` (e.g. `Get_fab`, not `get_fab`).

---

## 01 - Create tenant(s)

Creates `fabricapi_tenant`. Supports multi-tenant via `for_each` (`var.tenants`) or legacy single-tenant variables.

- Use **`prefer=respond-sync`** (async is disabled in the current release).
- Requires **Terraform >= 1.5**.

### Sample commands

```bash
terraform -chdir=examples/decoupled/01-tenant apply -auto-approve \
  -state=states/e2e_tenant.tfstate \
  -var="tenant_name=tenant1" \
  -var="tenant_description=TF Get_fab test" \
  -var="max_gpus_allowed=8" \
  -var="prefer=respond-sync"
```

### Tenant deletion

```bash
terraform -chdir=examples/decoupled/01-tenant destroy -auto-approve \
  -state=states/e2e_tenant.tfstate \
  -var="tenant_name=tenant1" \
  -var="tenant_description=TF Get_fab test" \
  -var="max_gpus_allowed=8" \
  -var="prefer=respond-sync"
```

---

## 05 - Available servers (lookup)

Read-only `fabricapi_available_servers` data source — GET `/fabrics/{fabric}/available_servers`.

- Does **not** create or delete anything on the fabric.
- Output: `available_gpus` (server hostnames currently free).
- **Same state file can be reused forever** — each `apply` refreshes the list from the API.
- No `destroy` or `terraform state rm` needed for normal use.
- Use before `02-servers` (and before HBN VF flows) to pick a real hostname.

### Sample commands

```bash
terraform -chdir=examples/decoupled/05-available-servers apply -auto-approve \
  -state=states/e2e_available_servers.tfstate \
  -var="fabric_name=Get_fab"
```

Use a hostname from the `available_gpus` output in step `02` (example below uses `hgx-su00-h00`).

---

## 02 - Allocate / deallocate whole servers

Manages `fabricapi_tenant_servers` — PATCH tenant with `operation=ADD` or `DELETE`.

- Tenant must exist (`01-tenant`).
- Use real server hostnames (from `05-available-servers` or your fabric inventory).
- Set `shared=true` when the fabric uses shared GPU servers / per-GPU allocation.

### Sample commands — allocate (ADD)

```bash
terraform -chdir=examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_servers.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=ADD" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=true" \
  -var="prefer=respond-sync"
```

### Sample commands — deallocate (DELETE)

```bash
terraform -chdir=examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_servers.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=DELETE" \
  -var='servers=["hgx-su00-h00"]' \
  -var="prefer=respond-sync"
```

Tip: To deallocate all servers for a tenant, use `-var='servers=[]'`.

---

## 04 - Per-GPU allocations (shared / external fabrics)

Manages `fabricapi_gpu_allocations` — POST `/fabrics/{fabric}/tenants/{tenant}/gpuAllocations`.

**Prerequisites:**

1. Tenant exists (`01-tenant`).
2. Server is already attached to the tenant (`02-servers` ADD).
3. Fabric is **externally managed** (`is_ones_controlled=false`). ONES-controlled fabrics use whole-server PATCH instead; `gpuAllocations` is rejected.

**Request shape** (sent by the provider):

```json
{
  "operation": "ADD",
  "suid": {
    "0": {
      "hgx-su00-h00": { "gpus": ["G6", "G7"] }
    }
  }
}
```

- `operation`: `ADD` or `DELETE` (also accepts `REMOVE` as alias for DELETE).
- `allocations`: flat list in Terraform; flattened to API `suid` map (`suid` → hostname → GPU list).
- GPU ids are logical names (`G0` … `G7` depending on fabric); pass any subset the backend allows.

**State behavior:** same as `02-servers` — `operation=DELETE` keeps the resource in state; use `destroy` only when removing Terraform management entirely.

### Sample commands — allocate GPUs (ADD)

```bash
terraform -chdir=examples/decoupled/04-gpu-allocations apply -auto-approve \
  -state=states/e2e_gpu_alloc.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=ADD" \
  -var='allocations=[{suid=0,server="hgx-su00-h00",gpus=["G6","G7"]}]' \
  -var="prefer=respond-sync"
```

### Sample commands — deallocate GPUs (DELETE)

```bash
terraform -chdir=examples/decoupled/04-gpu-allocations apply -auto-approve \
  -state=states/e2e_gpu_alloc.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=DELETE" \
  -var='allocations=[{suid=0,server="hgx-su00-h00",gpus=["G6","G7"]}]' \
  -var="prefer=respond-sync"
```

---

## 04-gpus - External GPU ports (UFM / NMX-C)

Manages `fabricapi_tenant_gpus` — POST `/fabrics/{fabric}/tenants/{tenant}/gpus`. This is the
**external GPU-port** path for UFM / NMX-C fabrics, distinct from `04-gpu-allocations`
(`gpuAllocations`, logical G0–G7 mapping): they hit different endpoints for different fabric models.

**Prerequisites:**

1. Tenant exists (`01-tenant`).
2. The GPU server exists on the fabric. For the two-step external flow, attach it first with
   `02-servers` using `shared=true` (leaves the E-W / UFM GPUs free for `/gpus`); with
   `shared=false` the E-W GPUs are already dedicated and `/gpus` returns `QUOTA_EXCEEDED`.
3. Fabric is **externally managed** (`is_ones_controlled=false`). ONES-controlled fabrics use whole-server PATCH instead; `gpus` is rejected.

**Request shape** (sent by the provider):

```json
{
  "operation": "ADD",
  "serverNames": ["SA-ZT-NDR-01"],
  "gpuIds": [1, 2, 3, 4],
  "membership": "full"
}
```

- `serverNames`: servers to assign/remove GPU ports on (Terraform `server_names`).
- `gpuIds` and `membership` are optional and omitted from the body when unset (see below).
  `membership` is UFM PKey membership (`full` or `limited`).

**`operation` — ADD / DELETE:** `ADD` assigns GPU ports, `DELETE` releases them (`REMOVE` is
accepted as an alias for `DELETE`). `operation` is the only attribute that updates **in place** —
apply with `ADD`, then re-apply the *same* resource with `operation=DELETE` to release without a
destroy (all other attributes force replacement). On `DELETE` the provider re-checks the live
allocation first, so re-running `DELETE`, or destroying an already-released resource, is a safe
no-op instead of an error.

**`gpu_ids` — whole-server vs per-GPU:** omit it (default `[]`) to act on the **whole server** —
the provider sends no `gpuIds` field (`{"operation":"ADD","serverNames":["SA-ZT-NDR-01"]}`).
Provide a non-empty 1-based list (e.g. `[1,2,3,4]`) to target **specific GPUs**; it is passed
through as `gpuIds` and the API returns `gpuIdsProcessed` = the count. An explicit empty list is
rejected (empty ≠ "whole server"). Per-GPU targeting is what lets two tenants share one physical
server on disjoint `gpu_ids`; overlapping `gpu_ids` are rejected with `GPU_ALREADY_ALLOCATED`.

Behaviour differs by fabric: on **NS+EW** a `/gpus` allocation shows only in `ufmAllocatedPorts`
(the tenant's `gpusAllocated` stays from the NS side); on **EW-IBOnly** it moves both.

### Sample commands — assign GPU ports (ADD)

```bash
terraform -chdir=examples/decoupled/04-gpus apply -auto-approve \
  -state=states/e2e_tenant_gpus.tfstate \
  -var="tenant_fabric=Fabric" \
  -var="tenant_name=tenant1" \
  -var="operation=ADD" \
  -var='server_names=["SA-ZT-NDR-01"]'
# per-GPU instead of whole server: add -var='gpu_ids=[1,2,3,4]'
# UFM membership: add -var="membership=full"
```

### Sample commands — release GPU ports (DELETE)

```bash
# In-place release (keeps the resource in state): flip operation to DELETE
terraform -chdir=examples/decoupled/04-gpus apply -auto-approve \
  -state=states/e2e_tenant_gpus.tfstate \
  -var="tenant_fabric=Fabric" \
  -var="tenant_name=tenant1" \
  -var="operation=DELETE" \
  -var='server_names=["SA-ZT-NDR-01"]'

# Or remove Terraform management entirely:
terraform -chdir=examples/decoupled/04-gpus destroy -auto-approve \
  -state=states/e2e_tenant_gpus.tfstate \
  -var="tenant_fabric=Fabric" \
  -var="tenant_name=tenant1" \
  -var="operation=ADD" \
  -var='server_names=["SA-ZT-NDR-01"]'
```

---

## Inventory sync (`fabricapi_inventory_sync`)

Forces an immediate UFM inventory reconcile — POST `/fabrics/{fabric}/inventorySync` — useful when
FM and UFM state have diverged (e.g. a stuck/orphaned PKey blocking tenant onboarding). It is an
action-style resource (runs on create; replace or re-apply to run again); there is no dedicated
example root. Add it to any root, for example:

```hcl
resource "fabricapi_inventory_sync" "this" {
  fabric = "Fabric" # optional; defaults to the provider fabric
}
```

```bash
terraform apply -auto-approve   # message = "Inventory sync completed successfully"
```

---

## 03 - VPC peering

Creates `fabricapi_vpcpeering`. Tenant should exist and networking should be ready.

### Sample commands

```bash
terraform -chdir=examples/decoupled/03-vpcpeering apply -auto-approve \
  -state=states/e2e_vpc.tfstate \
  -var="tenant_name=tenant1" \
  -var="vpcpeering_name=tf-vpcpeering-tenant1" \
  -var="delete_on_destroy=false"
```

Fabric is taken from `FABRIC_NAME` / provider `fabric` (set `Get_fab` in env before apply).

---

## Docker workflow (same commands, `/repo` paths)

Start the container from repo root (see `README.docker.md`), then inside the container.

### External / shared GPU fabric (available servers + per-GPU)

```bash
cd /repo

terraform -chdir=/repo/examples/decoupled/01-tenant apply -auto-approve \
  -state=states/e2e_tenant.tfstate \
  -var="tenant_name=tenant1" \
  -var="tenant_description=TF Get_fab test" \
  -var="max_gpus_allowed=8" \
  -var="prefer=respond-sync"

# Lookup free servers (GET available_servers) — no destroy needed
terraform -chdir=/repo/examples/decoupled/05-available-servers apply -auto-approve \
  -state=states/e2e_available_servers.tfstate \
  -var="fabric_name=Get_fab"

terraform -chdir=/repo/examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_servers.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=ADD" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=true" \
  -var="prefer=respond-sync"

# Per-GPU ADD (externally managed fabrics only)
terraform -chdir=/repo/examples/decoupled/04-gpu-allocations apply -auto-approve \
  -state=states/e2e_gpu_alloc.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=ADD" \
  -var='allocations=[{suid=0,server="hgx-su00-h00",gpus=["G6","G7"]}]' \
  -var="prefer=respond-sync"

# Per-GPU DELETE
terraform -chdir=/repo/examples/decoupled/04-gpu-allocations apply -auto-approve \
  -state=states/e2e_gpu_alloc.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=DELETE" \
  -var='allocations=[{suid=0,server="hgx-su00-h00",gpus=["G6","G7"]}]' \
  -var="prefer=respond-sync"

terraform -chdir=/repo/examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_servers.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=DELETE" \
  -var='servers=["hgx-su00-h00"]' \
  -var="prefer=respond-sync"

terraform -chdir=/repo/examples/decoupled/01-tenant destroy -auto-approve \
  -state=states/e2e_tenant.tfstate \
  -var="tenant_name=tenant1" \
  -var="tenant_description=TF Get_fab test" \
  -var="max_gpus_allowed=8" \
  -var="prefer=respond-sync"
```

### HBN fabric (VF list + assign / unbind)

Requires tenant + server attached first (same `01` / `02` pattern; use your HBN fabric name).

```bash
cd /repo

# Lookup VFs (GET vf-interfaces) — no destroy needed
terraform -chdir=/repo/examples/decoupled/06-vf-interfaces apply -auto-approve \
  -state=states/e2e_vf_interfaces.tfstate \
  -var="fabric_name=HBN_test_16" \
  -var="server_name=hgx-su00-h01"

# Bind VF (POST .../assign with tenantName)
terraform -chdir=/repo/examples/decoupled/07-vf-assign apply -auto-approve \
  -state=states/e2e_vf_assign.tfstate \
  -var="fabric_name=HBN_test_16" \
  -var="server_name=hgx-su00-h01" \
  -var="vf_id=vf4" \
  -var="tenant_name=Blue" \
  -var="prefer=respond-sync"

# Unbind VF (DELETE .../assign with tenantName)
terraform -chdir=/repo/examples/decoupled/07-vf-assign destroy -auto-approve \
  -state=states/e2e_vf_assign.tfstate \
  -var="fabric_name=HBN_test_16" \
  -var="server_name=hgx-su00-h01" \
  -var="vf_id=vf4" \
  -var="tenant_name=Blue" \
  -var="prefer=respond-sync"
```

---

## 06 - VF interfaces (HBN lookup)

Read-only `fabricapi_vf_interfaces` data source — GET `/fabrics/{fabric}/servers/{server}/vf-interfaces`.

- Lists DPU VF interfaces (`if_name`, `server_if`, `status`, `tenant_name`).
- Same pattern as `05-available-servers`: refresh by re-applying; no destroy needed.
- Pick a VF with `status=free` before `07-vf-assign`.

### Sample commands

```bash
terraform -chdir=examples/decoupled/06-vf-interfaces apply -auto-approve \
  -state=states/e2e_vf_interfaces.tfstate \
  -var="fabric_name=HBN_test_16" \
  -var="server_name=hgx-su00-h01"
```

---

## 07 - VF assign / unbind (HBN)

Manages `fabricapi_vf_assign` — POST/DELETE `/fabrics/{fabric}/servers/{server}/vf-interfaces/{vfId}/assign`.

**Request body (assign and unbind):**

```json
{ "tenantName": "Blue" }
```

Terraform always sends `tenant_name` on create and destroy so the call matches the documented API sample.

**Prerequisites:**

1. Tenant exists (`01-tenant`).
2. Server is attached to the tenant (`02-servers` ADD).
3. Fabric is DPU/HBN offload (server has VF interfaces).
4. VF is free (`06-vf-interfaces`).

### Sample commands — assign (apply)

```bash
terraform -chdir=examples/decoupled/07-vf-assign apply -auto-approve \
  -state=states/e2e_vf_assign.tfstate \
  -var="fabric_name=HBN_test_16" \
  -var="server_name=hgx-su00-h01" \
  -var="vf_id=vf4" \
  -var="tenant_name=Blue" \
  -var="prefer=respond-sync"
```

### Sample commands — unbind (destroy)

```bash
terraform -chdir=examples/decoupled/07-vf-assign destroy -auto-approve \
  -state=states/e2e_vf_assign.tfstate \
  -var="fabric_name=HBN_test_16" \
  -var="server_name=hgx-su00-h01" \
  -var="vf_id=vf4" \
  -var="tenant_name=Blue" \
  -var="prefer=respond-sync"
```

---

## General notes

- **Async / webhooks:** retained in variables for forward compatibility; **disabled in the current release**.
- **Lock file:** if you rebuild the provider and `init` fails on checksums, delete that root’s `.terraform.lock.hcl` and re-run `init`.
- **Tenant deletion:** deallocate VFs, servers (and per-GPU mappings if used) before destroy when the API requires an empty tenant.
- **Fabric name:** must match the API exactly (case-sensitive).
- **Available servers:** read-only lookup (`05`); re-apply to refresh — no destroy.
- **Per-GPU (`04`):** externally managed fabrics only; `operation=DELETE` keeps state; destroy removes Terraform management.
- **HBN VF unbind:** destroy sends `{"tenantName":"..."}` like the sample curl; also refuses destroy if GET shows the VF bound to a different tenant.
- **Lab license:** if PATCH/POST returns `403` for unlicensed devices, apply the FM DB license override for your fabric switch IPs before retrying.

See also: `README.docker.md`, `README.make.md`.
