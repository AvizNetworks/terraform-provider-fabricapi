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
- `FABRIC_NAME`: Fabric name/ID (example: `YOUR_FABRIC_NAME`)

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

## Sync vs Async vs Webhooks (important)

- **Sync (default)**: set `prefer=respond-sync` (or omit `prefer`).  
  - **No webhooks parameters are required**.
- **Async without webhooks**: set `prefer=respond-async` and `webhooks_enabled=false`.  
  - The provider will **poll** the backend operation until completion (using the returned operation id).
- **Async with webhooks**: set `prefer=respond-async` and `webhooks_enabled=true`.  
  - You must also set **both** `webhook_url` and `webhook_events`.

### VPC peering webhook support

VPC peering **does not currently integrate webhooks** in the backend. The resource schema keeps async/webhook fields for forward compatibility, but the create path uses a **sync** API call.

### Webhook parameters and events reference

These are the webhook-related parameters used in the example commands:

- **`prefer`**:
  - **Optional** (default is `respond-sync`)
  - Use `respond-async` only when you want async behavior
- **`webhooks_enabled`**:
  - **Optional** (default is `false`)
  - Only meaningful when `prefer=respond-async`
- **`webhook_url`**:
  - **Mandatory only when** `prefer=respond-async` **and** `webhooks_enabled=true`
- **`webhook_events`**:
  - **Mandatory only when** `prefer=respond-async` **and** `webhooks_enabled=true`

Events used in the docs:

- `tenant.create`
- `tenant.allocate`
- `tenant.deallocate`
- `tenant.delete`

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

## End-to-end commands (async + webhooks enabled)

The following commands demonstrate a full end-to-end workflow using asynchronous operations (`prefer=respond-async`) and webhooks (`webhooks_enabled=true`) for status updates.

Note: Terraform may show a warning that `-state` is deprecated, but these commands are preserved to match the workflow.

### 0) Configure connectivity (required)

```bash
export FABRIC_API_ENDPOINT="http://YOUR_FABRIC_API_HOST:8787"
export FABRIC_NAME="YOUR_FABRIC_NAME"
```

### 1) Tenant creation

```bash
mkdir -p ./examples/decoupled/01-tenant/states

terraform -chdir=./examples/decoupled/01-tenant apply -auto-approve \
  -state=states/e2e_async_tenant.tfstate \
  -var="tenant_name=tenw01" \
  -var="max_gpus_allowed=8" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var='webhook_url=http://YOUR_WEBHOOK_RECEIVER_HOST:8787/test/webhook-receiver' \
  -var='webhook_events=["tenant.create"]'
```

### 2) GPU allocation (ADD)

```bash
mkdir -p ./examples/decoupled/02-servers/states

terraform -chdir=./examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_async_servers.tfstate \
  -var="tenant_fabric=YOUR_FABRIC_NAME" \
  -var="tenant_name=tenw01" \
  -var="operation=ADD" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=false" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var='webhook_url=http://YOUR_WEBHOOK_RECEIVER_HOST:8787/test/webhook-receiver' \
  -var='webhook_events=["tenant.allocate"]'
```

### 3) VPC peering creation

```bash
mkdir -p ./examples/decoupled/03-vpcpeering/states

terraform -chdir=./examples/decoupled/03-vpcpeering apply -auto-approve \
  -state=states/e2e_vpc.tfstate \
  -var="tenant_name=tenw01" \
  -var="vpcpeering_name=tenw01-peer" \
  -var="delete_on_destroy=false"
```

### 4) GPU deallocation (DELETE)

```bash
terraform -chdir=./examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_async_servers.tfstate \
  -var="tenant_fabric=YOUR_FABRIC_NAME" \
  -var="tenant_name=tenw01" \
  -var="operation=DELETE" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=false" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var='webhook_url=http://YOUR_WEBHOOK_RECEIVER_HOST:8787/test/webhook-receiver' \
  -var='webhook_events=["tenant.deallocate"]'
```

Tip: To deallocate all servers for a tenant, use an empty list: `-var='servers=[]'`. Avoid using `servers=[""]`.

### 5) Tenant deletion

```bash
terraform -chdir=./examples/decoupled/01-tenant destroy -auto-approve \
  -state=states/e2e_async_tenant.tfstate \
  -var="tenant_name=tenw01" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var='webhook_url=http://YOUR_WEBHOOK_RECEIVER_HOST:8787/test/webhook-receiver' \
  -var='webhook_events=["tenant.delete"]'
```

### 6) VPC peering state removal (state-only)

Use this command to instruct Terraform to forget the VPC peering object in its state file, leaving the resource intact in the Fabric API.

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
- **Async + webhook errors**: when using `prefer=respond-async` and `webhooks_enabled=true`, you must set both `webhook_url` and `webhook_events`.
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

