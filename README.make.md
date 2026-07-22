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

- `examples/decoupled/01-tenant`
- `examples/decoupled/02-servers`
- `examples/decoupled/03-vpcpeering`
- `examples/decoupled/04-gpu-allocations` — per-GPU mapping (POST `gpuAllocations`)
- `examples/decoupled/05-available-servers` — free server lookup (GET `available_servers`)

### One-time init (per root)

```bash
terraform -chdir=examples/decoupled/01-tenant init -upgrade
terraform -chdir=examples/decoupled/02-servers init -upgrade
terraform -chdir=examples/decoupled/03-vpcpeering init -upgrade
terraform -chdir=examples/decoupled/04-gpu-allocations init -upgrade
terraform -chdir=examples/decoupled/05-available-servers init -upgrade
```

## End-to-end commands

The following commands demonstrate a full end-to-end workflow. The examples use `prefer=respond-sync` (you can omit `prefer` where the defaults match).

Note: Terraform may show a warning that `-state` is deprecated, but these commands are preserved to match the workflow.

Example values below use **`Get_fab`** (fabric), **`tenant1`** (tenant), and **`hgx-su00-h00`** (server)—substitute your real names from `GET /fabrics` and `05-available-servers` output.

### 0) Configure connectivity (required)

```bash
export FABRIC_API_ENDPOINT="https://10.4.5.132:8089"
export FABRIC_NAME="Get_fab"
export FABRIC_API_USERNAME="superadmin"
export FABRIC_API_PASSWORD="YOUR_PASSWORD"
export FABRICAPI_INSECURE_TLS=1
```

### State directories (one-time)

Create the local `states/` directories under each decoupled example **before** the first `apply` that uses those paths.

Run these `mkdir` commands only the **first** time you use the provider with these examples, or if those directories (or their parent trees) were removed. You do **not** need to run them again for routine applies or destroys.

```bash
mkdir -p ./examples/decoupled/01-tenant/states
mkdir -p ./examples/decoupled/02-servers/states
mkdir -p ./examples/decoupled/03-vpcpeering/states
mkdir -p ./examples/decoupled/04-gpu-allocations/states
mkdir -p ./examples/decoupled/05-available-servers/states
```

### How state files relate to tenants

Each decoupled root uses **its own** `-state=...` file. Together they describe **one tenant** when names and paths stay aligned.

| Root | Typical state file | Notes |
|------|-------------------|-------|
| `01-tenant` | `e2e_tenant.tfstate` | Managed resource |
| `02-servers` | `e2e_servers.tfstate` | Whole-server allocate/deallocate |
| `04-gpu-allocations` | `e2e_gpu_alloc.tfstate` | Per-GPU ADD/DELETE |
| `05-available-servers` | `e2e_available_servers.tfstate` | Read-only lookup; refreshed each apply |
| `03-vpcpeering` | `e2e_vpc.tfstate` | VPC peering |

- **Same tenant, same workflow**: reuse state paths and the same `tenant_name` / `tenant_fabric`.
- **New tenant or clean slate**: use new state filenames under `states/`.
- **Available servers**: no destroy or `state rm` needed — re-apply to refresh the list.
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

### Available servers (lookup)

Read-only data source — lists free GPU server hostnames. Run before whole-server allocate to pick a hostname. Same state file can be reused; each apply refreshes the list.

```bash
terraform -chdir=./examples/decoupled/05-available-servers apply -auto-approve \
  -state=states/e2e_available_servers.tfstate \
  -var="fabric_name=Get_fab"
```

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

### Per-GPU allocation (ADD)

Requires server attached in the previous step. Externally managed fabrics only.

```bash
terraform -chdir=./examples/decoupled/04-gpu-allocations apply -auto-approve \
  -state=states/e2e_gpu_alloc.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=ADD" \
  -var='allocations=[{suid=0,server="hgx-su00-h00",gpus=["G6","G7"]}]' \
  -var="prefer=respond-sync"
```

### Per-GPU deallocation (DELETE)

```bash
terraform -chdir=./examples/decoupled/04-gpu-allocations apply -auto-approve \
  -state=states/e2e_gpu_alloc.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=DELETE" \
  -var='allocations=[{suid=0,server="hgx-su00-h00",gpus=["G6","G7"]}]' \
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

### Lock file cleanup commands (only if you rebuilt the provider)

Usually you do **not** need to delete `.terraform.lock.hcl`. Only do this if Terraform reports a provider checksum mismatch after rebuilding the provider binary:

```bash
rm -f ./examples/decoupled/01-tenant/.terraform.lock.hcl
rm -f ./examples/decoupled/02-servers/.terraform.lock.hcl
rm -f ./examples/decoupled/03-vpcpeering/.terraform.lock.hcl
rm -f ./examples/decoupled/04-gpu-allocations/.terraform.lock.hcl
rm -f ./examples/decoupled/05-available-servers/.terraform.lock.hcl

terraform -chdir=./examples/decoupled/01-tenant init -upgrade
terraform -chdir=./examples/decoupled/02-servers init -upgrade
terraform -chdir=./examples/decoupled/03-vpcpeering init -upgrade
terraform -chdir=./examples/decoupled/04-gpu-allocations init -upgrade
terraform -chdir=./examples/decoupled/05-available-servers init -upgrade
```

