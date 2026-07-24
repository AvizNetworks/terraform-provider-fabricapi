# Terraform Provider `fabricapi` — Local install (Make-based)

This guide is for users who want to run Terraform on their **host machine** and install the provider binary into Terraform’s **local plugin directory** using `make install`.

If you prefer a containerized workflow (Terraform + provider inside Docker), see `README.docker.md`.

## Prerequisites

- Terraform **>= 1.5**
- Go (version compatible with `go.mod`)
- `make`
- Network access to your Fabric API endpoint

## Install the provider locally

Clone the repo and install the provider:

```bash
git clone https://github.com/AvizNetworks/terraform-provider-fabricapi
cd terraform-provider-fabricapi
make install
```

This compiles the provider and places the binary into Terraform’s local plugin directory so `terraform init` can locate it:

`~/.terraform.d/plugins/registry.terraform.io/local/fabricapi/<VERSION>/<OS>_<ARCH>/`

Notes:

- The version defaults to `VERSION=1.0.0` (see `Makefile`). Override if needed:

```bash
make install VERSION=1.0.1
```

- If you rebuild the provider binary, Terraform may reject it due to stale checksums in `.terraform.lock.hcl` inside an example root. If you hit that, delete the lock file in that root and re-run `terraform init`.

## Configure access (required)

The provider is configured primarily via environment variables.

### Endpoint

- `FABRIC_API_ENDPOINT`: Fabric API base URL (example: `http://localhost:8787`)
- `FABRIC_NAME`: Fabric name/ID (example: `fabric01`)

### Important: fabric must already exist

Before running Terraform, ensure the **Fabric** you set in `FABRIC_NAME` already exists in your Fabric system. Terraform will use this value to construct API paths.

### Authentication (choose one)

**Option A: access token (JWT)**

```bash
export FABRIC_API_ACCESS_TOKEN="eyJ..."

# Optional: if you also want automatic refresh on 401, provide a refresh token
export FABRIC_API_REFRESH_TOKEN="..."
```

**Option B: username/password**

```bash
export FABRIC_API_USERNAME="YOUR_USERNAME"
export FABRIC_API_PASSWORD="YOUR_PASSWORD"

# Optional: if auth is on a different base URL (defaults to FABRIC_API_ENDPOINT)
export FABRIC_API_AUTH_ENDPOINT="https://localhost:8089"
```

### TLS (only for lab/testing)

If your auth endpoint uses a self-signed certificate, you can disable TLS verification:

```bash
export FABRICAPI_INSECURE_TLS=1
```

Avoid using this in production.

## Recommended examples workflow (decoupled)

The most copy/paste friendly workflow is under `examples/decoupled/` where each action is a separate Terraform root:

- `examples/decoupled/01-tenant` — tenant lifecycle
- `examples/decoupled/02-servers` — whole-server allocate/deallocate (PATCH tenant)
- `examples/decoupled/03-vpcpeering` — VPC peering
- `examples/decoupled/04-gpu-allocations` — per-GPU ADD/DELETE (POST `gpuAllocations`; externally managed fabrics)
- `examples/decoupled/05-available-servers` — free server lookup (GET `available_servers`)
- `examples/decoupled/06-vf-interfaces` — HBN VF lookup (GET `vf-interfaces`)
- `examples/decoupled/07-vf-assign` — HBN VF bind/unbind (POST/DELETE `vf-interfaces/{vfId}/assign`)

### One-time init (per root)

```bash
terraform -chdir=examples/decoupled/01-tenant init -upgrade
terraform -chdir=examples/decoupled/02-servers init -upgrade
terraform -chdir=examples/decoupled/03-vpcpeering init -upgrade
terraform -chdir=examples/decoupled/04-gpu-allocations init -upgrade
terraform -chdir=examples/decoupled/05-available-servers init -upgrade
terraform -chdir=examples/decoupled/06-vf-interfaces init -upgrade
terraform -chdir=examples/decoupled/07-vf-assign init -upgrade
```

## End-to-end commands

The following commands demonstrate a full end-to-end workflow. The examples use `prefer=respond-sync` (you can omit `prefer` where the defaults match).

Note: Terraform may show a warning that `-state` is deprecated, but these commands are preserved to match the workflow.

**Example values (external / shared GPU fabric):** **`Get_fab`**, **`tenant1`**, **`hgx-su00-h00`**.  
**Example values (HBN / DPU fabric):** **`HBN_test_16`**, **`Blue`**, **`hgx-su00-h01`**, **`vf4`**.  
Substitute real names from `GET /fabrics`, `05-available-servers`, and `06-vf-interfaces` output.

### 0) Configure connectivity (required)

```bash
export FABRIC_API_ENDPOINT="https://10.4.5.132:8089"
export FABRIC_NAME="Get_fab"
export FABRIC_API_USERNAME="superadmin"
export FABRIC_API_PASSWORD="YOUR_PASSWORD"
export FABRICAPI_INSECURE_TLS=1
```

For HBN testing, set `FABRIC_NAME` to your HBN fabric (e.g. `HBN_test_16`) instead.

### State directories (one-time)

Create the local `states/` directories under each decoupled example **before** the first `apply` that uses those paths.

Run these `mkdir` commands only the **first** time you use the provider with these examples, or if those directories (or their parent trees) were removed. You do **not** need to run them again for routine applies or destroys.

```bash
mkdir -p ./examples/decoupled/01-tenant/states
mkdir -p ./examples/decoupled/02-servers/states
mkdir -p ./examples/decoupled/03-vpcpeering/states
mkdir -p ./examples/decoupled/04-gpu-allocations/states
mkdir -p ./examples/decoupled/05-available-servers/states
mkdir -p ./examples/decoupled/06-vf-interfaces/states
mkdir -p ./examples/decoupled/07-vf-assign/states
```

### How state files relate to tenants

Each decoupled root uses **its own** `-state=...` file. Together they describe **one tenant** when names and paths stay aligned.

| Root | Typical state file | Notes |
|------|-------------------|-------|
| `01-tenant` | `e2e_tenant.tfstate` | Managed resource |
| `02-servers` | `e2e_servers.tfstate` | Whole-server allocate/deallocate |
| `04-gpu-allocations` | `e2e_gpu_alloc.tfstate` | Per-GPU ADD/DELETE (external fabrics) |
| `05-available-servers` | `e2e_available_servers.tfstate` | Read-only lookup; refreshed each apply |
| `06-vf-interfaces` | `e2e_vf_interfaces.tfstate` | Read-only HBN VF lookup; refreshed each apply |
| `07-vf-assign` | `e2e_vf_assign.tfstate` | HBN VF assign; destroy to unbind |
| `03-vpcpeering` | `e2e_vpc.tfstate` | VPC peering |

- **Same tenant, same workflow**: reuse state paths and the same `tenant_name` / `tenant_fabric`.
- **New tenant or clean slate**: use new state filenames under `states/`.
- **Available servers / VF interfaces**: no destroy or `state rm` needed — re-apply to refresh the list.
- **VPC peering again**: see **VPC peering state removal** below if stale state blocks a new peering run.

### 1) Tenant creation

```bash
terraform -chdir=./examples/decoupled/01-tenant apply -auto-approve \
  -state=states/e2e_tenant.tfstate \
  -var="tenant_name=tenant1" \
  -var="tenant_description=TF Get_fab test" \
  -var="max_gpus_allowed=8" \
  -var="prefer=respond-sync"
```

### Available servers (lookup) — GET `available_servers`

Read-only data source (`fabricapi_available_servers`) — lists free GPU server hostnames for a fabric.

- Run **before** whole-server allocate to discover a hostname.
- Does **not** create or delete anything on the fabric.
- Same state file can be reused forever; each `apply` refreshes the list.
- No `destroy` or `terraform state rm` needed.

```bash
terraform -chdir=./examples/decoupled/05-available-servers apply -auto-approve \
  -state=states/e2e_available_servers.tfstate \
  -var="fabric_name=Get_fab"
```

Use a hostname from the `available_gpus` output in the next step.

### 2) GPU allocation — whole server (ADD)

```bash
terraform -chdir=./examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_servers.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=ADD" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=true" \
  -var="prefer=respond-sync"
```

Set `shared=true` for shared / per-GPU and HBN flows.

### Per-GPU allocation (ADD) — POST `gpuAllocations`

Maps individual GPUs (e.g. G6, G7) on servers **already attached** to the tenant.

**Prerequisites:**

1. Tenant exists (`01-tenant`).
2. Server attached via `02-servers` ADD.
3. Fabric is **externally managed** (`is_ones_controlled=false`). ONES-controlled fabrics reject `gpuAllocations`.

```bash
terraform -chdir=./examples/decoupled/04-gpu-allocations apply -auto-approve \
  -state=states/e2e_gpu_alloc.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=ADD" \
  -var='allocations=[{suid=0,server="hgx-su00-h00",gpus=["G6","G7"]}]' \
  -var="prefer=respond-sync"
```

### Per-GPU deallocation (DELETE) — POST `gpuAllocations`

Same resource and state file; only `operation` changes. Keeps the resource in Terraform state (day-2 reuse). Use `destroy` only when removing Terraform management entirely.

```bash
terraform -chdir=./examples/decoupled/04-gpu-allocations apply -auto-approve \
  -state=states/e2e_gpu_alloc.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=DELETE" \
  -var='allocations=[{suid=0,server="hgx-su00-h00",gpus=["G6","G7"]}]' \
  -var="prefer=respond-sync"
```

### HBN — VF interfaces (lookup) — GET `vf-interfaces`

Read-only data source (`fabricapi_vf_interfaces`) — lists DPU VF interfaces for a GPU server.

- Same pattern as available servers: re-apply to refresh; no destroy needed.
- Pick a VF with `status=free` before assign (`vf_id` may be `server_if` like `vf4` or DPU `if_name`).

```bash
terraform -chdir=./examples/decoupled/06-vf-interfaces apply -auto-approve \
  -state=states/e2e_vf_interfaces.tfstate \
  -var="fabric_name=HBN_test_16" \
  -var="server_name=hgx-su00-h01"
```

### HBN — VF assign (bind) — POST `.../assign`

Binds a VF to the tenant VLAN. Body includes `tenantName` (required).

**Prerequisites:** tenant exists, server is attached to the tenant, fabric is DPU/HBN offload, VF is free.

```bash
terraform -chdir=./examples/decoupled/07-vf-assign apply -auto-approve \
  -state=states/e2e_vf_assign.tfstate \
  -var="fabric_name=HBN_test_16" \
  -var="server_name=hgx-su00-h01" \
  -var="vf_id=vf4" \
  -var="tenant_name=Blue" \
  -var="prefer=respond-sync"
```

### HBN — VF unbind — DELETE `.../assign`

Destroy sends `{"tenantName":"..."}` in the body (matches the Fabric API sample).

```bash
terraform -chdir=./examples/decoupled/07-vf-assign destroy -auto-approve \
  -state=states/e2e_vf_assign.tfstate \
  -var="fabric_name=HBN_test_16" \
  -var="server_name=hgx-su00-h01" \
  -var="vf_id=vf4" \
  -var="tenant_name=Blue" \
  -var="prefer=respond-sync"
```

### 3) VPC peering creation

```bash
terraform -chdir=./examples/decoupled/03-vpcpeering apply -auto-approve \
  -state=states/e2e_vpc.tfstate \
  -var="tenant_name=tenant1" \
  -var="vpcpeering_name=tf-vpcpeering-tenant1" \
  -var="delete_on_destroy=false"
```

Fabric comes from `FABRIC_NAME` / provider `fabric`.

### 4) GPU deallocation — whole server (DELETE)

```bash
terraform -chdir=./examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_servers.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=DELETE" \
  -var='servers=["hgx-su00-h00"]' \
  -var="prefer=respond-sync"
```

Tip: To deallocate all servers for a tenant, use an empty list: `-var='servers=[]'`. Avoid using `servers=[""]`.

### 5) Tenant deletion

Unbind VFs and deallocate servers (and per-GPU mappings if used) before destroy when the API requires an empty tenant.

```bash
terraform -chdir=./examples/decoupled/01-tenant destroy -auto-approve \
  -state=states/e2e_tenant.tfstate \
  -var="tenant_name=tenant1" \
  -var="tenant_description=TF Get_fab test" \
  -var="max_gpus_allowed=8" \
  -var="prefer=respond-sync"
```

### 6) VPC peering state removal (state-only)

Use this command to instruct Terraform to forget the VPC peering object in its state file, leaving the resource intact in the Fabric API. You typically need this when you want Terraform to **manage peering again** from a clean slate (see **How state files relate to tenants** above).

```bash
terraform -chdir=./examples/decoupled/03-vpcpeering state rm \
  -state=states/e2e_vpc.tfstate \
  fabricapi_vpcpeering.this
```

## Alternative examples

- `examples/` (single Terraform root): `examples/main.tf`
- Identity reuse: `examples/fabric-identity.md` (create `examples/fabric.identity.auto.tfvars` once and avoid repeating identity flags)

## Troubleshooting

- **Provider not found**: re-run `make install`, then re-run `terraform init -upgrade` in the example root.
- **Checksum mismatch / lock file issues**: delete `.terraform.lock.hcl` in that root and re-run `terraform init`.
- **GOOS/GOARCH not detected**: ensure `go` is in `PATH` (`go version` should work).
- **Connectivity failures**: verify `FABRIC_API_ENDPOINT` and ensure your machine can reach the endpoint.
- **403 LICENSED**: devices in the fabric are not licensed. Lab-only: override via `ones-fm-db` on the FM host, then retry.
- **HBN assign `SERVER_NOT_IN_TENANT`**: attach the server with `02-servers` ADD first.

### Lock file cleanup commands (only if you rebuilt the provider)

Usually you do **not** need to delete `.terraform.lock.hcl`. Only do this if Terraform reports a provider checksum mismatch after rebuilding the provider binary:

```bash
rm -f ./examples/decoupled/01-tenant/.terraform.lock.hcl
rm -f ./examples/decoupled/02-servers/.terraform.lock.hcl
rm -f ./examples/decoupled/03-vpcpeering/.terraform.lock.hcl
rm -f ./examples/decoupled/04-gpu-allocations/.terraform.lock.hcl
rm -f ./examples/decoupled/05-available-servers/.terraform.lock.hcl
rm -f ./examples/decoupled/06-vf-interfaces/.terraform.lock.hcl
rm -f ./examples/decoupled/07-vf-assign/.terraform.lock.hcl

terraform -chdir=./examples/decoupled/01-tenant init -upgrade
terraform -chdir=./examples/decoupled/02-servers init -upgrade
terraform -chdir=./examples/decoupled/03-vpcpeering init -upgrade
terraform -chdir=./examples/decoupled/04-gpu-allocations init -upgrade
terraform -chdir=./examples/decoupled/05-available-servers init -upgrade
terraform -chdir=./examples/decoupled/06-vf-interfaces init -upgrade
terraform -chdir=./examples/decoupled/07-vf-assign init -upgrade
```

