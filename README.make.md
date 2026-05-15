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

### One-time init (per root)

```bash
terraform -chdir=examples/decoupled/01-tenant init -upgrade
terraform -chdir=examples/decoupled/02-servers init -upgrade
terraform -chdir=examples/decoupled/03-vpcpeering init -upgrade
```

## End-to-end commands

The following commands demonstrate a full end-to-end workflow. The examples use `prefer=respond-sync` (you can omit `prefer` where the defaults match).

Note: Terraform may show a warning that `-state` is deprecated, but these commands are preserved to match the workflow.

Example values such as **`fabric01`** (fabric), **`tenw01`** (tenant), and server names are illustrative—substitute your real names.

### 0) Configure connectivity (required)

```bash
export FABRIC_API_ENDPOINT="http://YOUR_FABRIC_API_HOST:8787"
export FABRIC_NAME="fabric01"
```

### State directories (one-time)

Create the local `states/` directories under each decoupled example **before** the first `apply` that uses those paths.

Run these `mkdir` commands only the **first** time you use the provider with these examples, or if those directories (or their parent trees) were removed. You do **not** need to run them again for routine applies or destroys.

```bash
mkdir -p ./examples/decoupled/01-tenant/states
mkdir -p ./examples/decoupled/02-servers/states
mkdir -p ./examples/decoupled/03-vpcpeering/states
```

### How state files relate to tenants

Each decoupled root uses **its own** `-state=...` file (`e2e_tenant.tfstate`, `e2e_servers.tfstate`, `e2e_vpc.tfstate`). They are not one combined Terraform state; together they describe **one tenant** if you keep names and paths aligned.

- **Same tenant, same workflow**: reuse those state paths and always pass the **same** `tenant_name` (and the same `tenant_fabric` on server commands) as in the tenant step. The servers state tracks allocations **for that tenant**—changing `tenant_name` while keeping the old servers state file will confuse Terraform unless you intentionally reset or replace state.
- **A different tenant or a clean slate**: use **new** state filenames under `states/` (or new directories). Do not reuse the same `e2e_*.tfstate` files for another tenant; Terraform would still think the old resources belong to this configuration.
- **VPC peering again**: if Terraform must stop tracking an existing peer (for example you used `delete_on_destroy=false` or you need a fresh apply), run **VPC peering state removal** below so the next `apply` is not blocked by stale state. Skipping that when you expect a new peering run can lead to errors or the wrong object being tracked.

### 1) Tenant creation

```bash
terraform -chdir=./examples/decoupled/01-tenant apply -auto-approve \
  -state=states/e2e_tenant.tfstate \
  -var="tenant_name=tenw01" \
  -var="max_gpus_allowed=8" \
  -var="prefer=respond-sync"
```

### 2) GPU allocation (ADD)

```bash
terraform -chdir=./examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_servers.tfstate \
  -var="tenant_fabric=fabric01" \
  -var="tenant_name=tenw01" \
  -var="operation=ADD" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=false" \
  -var="prefer=respond-sync"
```

### 3) VPC peering creation

```bash
terraform -chdir=./examples/decoupled/03-vpcpeering apply -auto-approve \
  -state=states/e2e_vpc.tfstate \
  -var="tenant_name=tenw01" \
  -var="vpcpeering_name=tenw01-peer" \
  -var="delete_on_destroy=false"
```

### 4) GPU deallocation (DELETE)

```bash
terraform -chdir=./examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_servers.tfstate \
  -var="tenant_fabric=fabric01" \
  -var="tenant_name=tenw01" \
  -var="operation=DELETE" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=false" \
  -var="prefer=respond-sync"
```

Tip: To deallocate all servers for a tenant, use an empty list: `-var='servers=[]'`. Avoid using `servers=[""]`.

### 5) Tenant deletion

```bash
terraform -chdir=./examples/decoupled/01-tenant destroy -auto-approve \
  -state=states/e2e_tenant.tfstate \
  -var="tenant_name=tenw01" \
  -var="prefer=respond-sync"
```

### 6) VPC peering state removal (state-only)

Use this command to instruct Terraform to forget the VPC peering object in its state file, leaving the resource intact in the Fabric API. You typically need this when you want Terraform to **manage peering again** from a clean slate (see **How state files relate to tenants** above).

```bash
terraform -chdir=./examples/decoupled/03-vpcpeering state rm \
  -state=states/e2e_vpc.tfstate \
  fabricapi_vpcpeering.this
```

## End-to-end commands (async + webhooks)

This section mirrors the sync workflow above using **`prefer=respond-async`** and optional webhooks for **tenant create** and **GPU allocate/deallocate**. **VPC peering remains sync-only** (FM does not implement async on that API).

### What is async?

| Mode | HTTP `Prefer` header | FM behaviour | Terraform behaviour |
|------|----------------------|--------------|---------------------|
| **Sync** (default) | `respond-sync` or omitted | Request blocks until work finishes; returns **200** | Apply waits on the HTTP response |
| **Async** | `respond-async` | FM returns **202** + `operationId`; work runs in background | Provider polls **`GET /operations/{id}`** until **SUCCESS** or **FAILED** |

**Webhooks (optional, async only):** When `webhooks_enabled=true`, FM POSTs completion to your **`webhook_url`** (HMAC-signed). Run a standalone HTTP listener (e.g. `webhook_tester.py`) on the host FM can reach. Terraform still completes apply based on operation polling, not the webhook SSE stream.

**Valid `webhook_events`:** `tenant.create`, `tenant.delete`, `tenant.allocate`, `tenant.deallocate`

### Webhook listener (host — before Terraform)

Pick a free port on the FM-reachable host (example **5001**): `ss -tln | grep ':5001'`. Leave this process running during apply:

```bash
pip3 install flask
python3 webhook_tester.py --host 0.0.0.0 --port 5001
```

Optional — watch events in a second terminal:

```bash
curl -N -s -H "Accept: text/event-stream" \
  "http://YOUR_FM_HOST:5001/test/webhook-receiver/stream"
```

Use **`http://`** for the webhook URL (not FM’s `https://...:8089`) to avoid TLS trust issues in lab setups.

### 0) Configure connectivity (required)

Same as sync — set endpoint, fabric, and auth (username/password or access token). Example:

```bash
export FABRIC_API_ENDPOINT="https://YOUR_FABRIC_API_HOST:8089"
export FABRIC_NAME="fabric01"
export FABRIC_API_USERNAME="YOUR_USERNAME"
export FABRIC_API_PASSWORD="YOUR_PASSWORD"
export FABRICAPI_INSECURE_TLS=1
```

Webhook and tenant vars for the commands below (tenant name under 9 characters for lab naming):

```bash
export WEBHOOK_URL="http://YOUR_FM_HOST:5001/test/webhook-receiver"
export TENANT="tenw01"
export TENANT_DESC="TF async webhook tenant"
export MAX_GPUS=8
```

Use separate state files from the sync e2e run (example: `e2e_async_*.tfstate` under the same `states/` directories).

### 1) Tenant creation (async + webhooks)

```bash
terraform -chdir=./examples/decoupled/01-tenant apply -auto-approve \
  -state=states/e2e_async_tenant.tfstate \
  -var="tenant_name=${TENANT}" \
  -var="tenant_description=${TENANT_DESC}" \
  -var="max_gpus_allowed=${MAX_GPUS}" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var="webhook_url=${WEBHOOK_URL}" \
  -var='webhook_events=["tenant.create"]'
```

### 2) GPU allocation (ADD, async + webhooks)

```bash
terraform -chdir=./examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_async_servers.tfstate \
  -var="tenant_fabric=fabric01" \
  -var="tenant_name=${TENANT}" \
  -var="operation=ADD" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=true" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var="webhook_url=${WEBHOOK_URL}" \
  -var='webhook_events=["tenant.allocate"]'
```

### 3) VPC peering creation (sync only)

```bash
terraform -chdir=./examples/decoupled/03-vpcpeering apply -auto-approve \
  -state=states/e2e_async_vpc.tfstate \
  -var="tenant_name=${TENANT}" \
  -var="vpcpeering_name=${TENANT}-peer" \
  -var="prefer=respond-sync" \
  -var="webhooks_enabled=false" \
  -var="delete_on_destroy=false"
```

### 4) GPU deallocation (DELETE, async + webhooks)

```bash
terraform -chdir=./examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_async_servers.tfstate \
  -var="tenant_fabric=fabric01" \
  -var="tenant_name=${TENANT}" \
  -var="operation=DELETE" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=false" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var="webhook_url=${WEBHOOK_URL}" \
  -var='webhook_events=["tenant.deallocate"]'
```

### 5) Tenant deletion (async + webhooks)

```bash
terraform -chdir=./examples/decoupled/01-tenant destroy -auto-approve \
  -state=states/e2e_async_tenant.tfstate \
  -var="tenant_name=${TENANT}" \
  -var="tenant_description=${TENANT_DESC}" \
  -var="max_gpus_allowed=${MAX_GPUS}" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var="webhook_url=${WEBHOOK_URL}" \
  -var='webhook_events=["tenant.delete"]'
```

### 6) VPC peering destroy (optional)

```bash
terraform -chdir=./examples/decoupled/03-vpcpeering destroy -auto-approve \
  -state=states/e2e_async_vpc.tfstate \
  -var="tenant_name=${TENANT}" \
  -var="vpcpeering_name=${TENANT}-peer"
```

### Verify webhooks (optional)

```bash
curl -s "${WEBHOOK_URL}" | python3 -m json.tool
curl -s "${WEBHOOK_URL}/summary" | python3 -m json.tool
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

terraform -chdir=./examples/decoupled/01-tenant init -upgrade
terraform -chdir=./examples/decoupled/02-servers init -upgrade
terraform -chdir=./examples/decoupled/03-vpcpeering init -upgrade
```

