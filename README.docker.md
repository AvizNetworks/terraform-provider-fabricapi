# Terraform Provider `fabricapi` — Docker workflow

This guide is for users who want to run Terraform **inside a Docker container** that already includes the `fabricapi` provider.

If you prefer to run Terraform on your host and install the provider locally using `make install`, see `README.make.md`.

## Prerequisites

- Docker
- Network access from the container to your Fabric API endpoint

## Clone the repo (required for this workflow)

This Docker workflow is **repo-mounted** (it mounts your local checkout into the container at `/repo`), so you must have the repository on your machine:

```bash
git clone https://github.com/AvizNetworks/terraform-provider-fabricapi
cd terraform-provider-fabricapi
```

## Build the image

From repo root:

```bash
./docker-build.sh
```

This builds `terraform-fabricapi:latest` by default.

Alternatively, you can build directly:

```bash
docker build -t terraform-fabricapi:latest .
```

Optional environment variables:

- `IMAGE` (default: `terraform-fabricapi:latest`)
- `PROVIDER_VERSION` (default: `1.0.0`)
- `DOCKER_BUILDKIT` (default: `0`)
- `CACHEBUST` (default: current timestamp)

## Run the container (repo-mounted workflow)

This workflow mounts your working copy into the container at `/repo` and then opens an interactive shell inside the container.
After the container starts, you will run the Terraform commands **inside that container shell**.

```bash
docker run -it --rm \
  -e FABRIC_API_ENDPOINT="https://YOUR_FABRIC_API_HOST:8089" \
  -e FABRIC_API_AUTH_ENDPOINT="https://YOUR_FABRIC_API_HOST:8089" \
  -e FABRIC_NAME="YOUR_FABRIC_NAME" \
  # Option A: access token (JWT)
  # -e FABRIC_API_ACCESS_TOKEN="eyJ..." \
  # -e FABRIC_API_REFRESH_TOKEN="..." \
  # Option B: username/password
  -e FABRIC_API_USERNAME="YOUR_USERNAME" \
  -e FABRIC_API_PASSWORD="YOUR_PASSWORD" \
  -v "$PWD:/repo" \
  terraform-fabricapi:latest
```

### Important: fabric must already exist

Before running Terraform, ensure the **Fabric** you set in `FABRIC_NAME` already exists in your Fabric system. Terraform will use this value to construct API paths.

### Notes on networking and TLS (important)

- **Avoid `--network host` by default.** Only use it for local-only test setups where the endpoint is reachable only via host networking.
- If your auth endpoint uses a self-signed certificate (lab/testing), you can disable TLS verification by passing:

```bash
-e FABRICAPI_INSECURE_TLS=1
```

Avoid using this in production.

### Avoiding file permission / ownership issues (recommended)

When you mount your repo into the container, Terraform will write `.terraform/`, `.terraform.lock.hcl`, and state files inside that mount.
To avoid root-owned files on your host, run the container as your user:

```bash
docker run -it --rm \
  -u "$(id -u):$(id -g)" \
  -e FABRIC_API_ENDPOINT="https://YOUR_FABRIC_API_HOST:8089" \
  -e FABRIC_API_AUTH_ENDPOINT="https://YOUR_FABRIC_API_HOST:8089" \
  -e FABRIC_NAME="YOUR_FABRIC_NAME" \
  -e FABRIC_API_USERNAME="YOUR_USERNAME" \
  -e FABRIC_API_PASSWORD="YOUR_PASSWORD" \
  -v "$PWD:/repo" \
  terraform-fabricapi:latest
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

## Run Terraform inside the container

Inside the container, use `/repo/...` paths. If your prompt starts in `/workspace`, run:

```bash
cd /repo
```

### Recommended: decoupled examples

Initialize each example root once:

```bash
terraform -chdir=/repo/examples/decoupled/01-tenant init -upgrade
terraform -chdir=/repo/examples/decoupled/02-servers init -upgrade
terraform -chdir=/repo/examples/decoupled/03-vpcpeering init -upgrade
```

## End-to-end commands (async + webhooks enabled) — inside the container

The following commands demonstrate a full end-to-end workflow using asynchronous operations (`prefer=respond-async`) and webhooks (`webhooks_enabled=true`) for status updates.

Note: Terraform may show a warning that `-state` is deprecated, but these commands are preserved to match the workflow.

```bash
# 0) Create local state directories once (per root)
mkdir -p /repo/examples/decoupled/01-tenant/states
mkdir -p /repo/examples/decoupled/02-servers/states
mkdir -p /repo/examples/decoupled/03-vpcpeering/states

# 1) Tenant creation
terraform -chdir=/repo/examples/decoupled/01-tenant apply -auto-approve \
  -state=states/e2e_async_tenant.tfstate \
  -var="tenant_name=tenw01" \
  -var="max_gpus_allowed=8" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var='webhook_url=http://YOUR_WEBHOOK_RECEIVER_HOST:8787/test/webhook-receiver' \
  -var='webhook_events=["tenant.create"]'

# 2) GPU allocation (ADD)
terraform -chdir=/repo/examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_async_servers.tfstate \
  -var="tenant_fabric=Tenant" \
  -var="tenant_name=tenw01" \
  -var="operation=ADD" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=false" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var='webhook_url=http://YOUR_WEBHOOK_RECEIVER_HOST:8787/test/webhook-receiver' \
  -var='webhook_events=["tenant.allocate"]'

# 3) VPC peering creation
terraform -chdir=/repo/examples/decoupled/03-vpcpeering apply -auto-approve \
  -state=states/e2e_vpc.tfstate \
  -var="tenant_name=tenw01" \
  -var="vpcpeering_name=tenw01-peer" \
  -var="delete_on_destroy=false"
```

### GPU deallocation (DELETE)

```bash
terraform -chdir=/repo/examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_async_servers.tfstate \
  -var="tenant_fabric=Tenant" \
  -var="tenant_name=tenw01" \
  -var="operation=DELETE" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=false" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var='webhook_url=http://YOUR_WEBHOOK_RECEIVER_HOST:8787/test/webhook-receiver' \
  -var='webhook_events=["tenant.deallocate"]'
```

Tip: To deallocate all servers for a tenant, use an empty list: `-var='servers=[]'`.

### Tenant deletion

```bash
terraform -chdir=/repo/examples/decoupled/01-tenant destroy -auto-approve \
  -state=states/e2e_async_tenant.tfstate \
  -var="tenant_name=tenw01" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var='webhook_url=http://YOUR_WEBHOOK_RECEIVER_HOST:8787/test/webhook-receiver' \
  -var='webhook_events=["tenant.delete"]'
```

### VPC peering state removal (state-only)

```bash
terraform -chdir=/repo/examples/decoupled/03-vpcpeering state rm \
  -state=states/e2e_vpc.tfstate \
  fabricapi_vpcpeering.this
```

## Troubleshooting

- **Container can’t reach API**: verify the endpoint is reachable from Docker networking. If you’re calling a host-local endpoint, try adding `--network host` (local testing only).
- **TLS errors with lab/self-signed certs**: set `FABRICAPI_INSECURE_TLS=1` (avoid in production).
- **Do I need to delete `.terraform.lock.hcl`?** Usually no. Only do this if you rebuilt the provider binary and Terraform reports a checksum mismatch.
  - Delete the lock file in the specific example root and re-init:

```bash
rm -f /repo/examples/decoupled/01-tenant/.terraform.lock.hcl
rm -f /repo/examples/decoupled/02-servers/.terraform.lock.hcl
rm -f /repo/examples/decoupled/03-vpcpeering/.terraform.lock.hcl

terraform -chdir=/repo/examples/decoupled/01-tenant init -upgrade
terraform -chdir=/repo/examples/decoupled/02-servers init -upgrade
terraform -chdir=/repo/examples/decoupled/03-vpcpeering init -upgrade
```

