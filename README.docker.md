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

Choose one authentication method and run **one** of the following commands.

**Option A: access token (JWT)**

```bash
docker run -it --rm \
  -e FABRIC_API_ENDPOINT="https://YOUR_FABRIC_API_HOST:8089" \
  -e FABRIC_API_AUTH_ENDPOINT="https://YOUR_FABRIC_API_HOST:8089" \
  -e FABRIC_NAME="YOUR_FABRIC_NAME" \
  -e FABRIC_API_ACCESS_TOKEN="eyJ..." \
  -e FABRIC_API_REFRESH_TOKEN="..." \
  -v "$PWD:/repo" \
  terraform-fabricapi:latest
```

**Option B: username/password**

```bash
docker run -it --rm \
  -e FABRIC_API_ENDPOINT="https://YOUR_FABRIC_API_HOST:8089" \
  -e FABRIC_API_AUTH_ENDPOINT="https://YOUR_FABRIC_API_HOST:8089" \
  -e FABRIC_NAME="YOUR_FABRIC_NAME" \
  -e FABRIC_API_USERNAME="YOUR_USERNAME" \
  -e FABRIC_API_PASSWORD="YOUR_PASSWORD" \
  -v "$PWD:/repo" \
  terraform-fabricapi:latest
```

### Important: fabric must already exist

Before running Terraform, ensure the **Fabric** you set in `FABRIC_NAME` already exists in your Fabric system. Terraform will use this value to construct API paths.

### Notes on networking and TLS (important)

- **Avoid `--network host` by default.** Only use it for local-only test setups where the endpoint is reachable only via host networking.
- If your auth endpoint uses a self-signed certificate (lab/testing), add:

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

## End-to-end commands — inside the container

The following sections walk through a full end-to-end workflow. The examples use `prefer=respond-sync` (you can omit `prefer` where the defaults match).

Note: Terraform may show a warning that `-state` is deprecated, but these commands are preserved to match the workflow.

### State directories (one-time)

Create the local `states/` directories under each decoupled example **before** the first `apply` that uses those paths.

Run these `mkdir` commands only the **first** time you use the provider with these examples, or if those directories (or their parent trees) were removed. You do **not** need to run them again for routine applies or destroys.

```bash
mkdir -p /repo/examples/decoupled/01-tenant/states
mkdir -p /repo/examples/decoupled/02-servers/states
mkdir -p /repo/examples/decoupled/03-vpcpeering/states
```

### How state files relate to tenants

Each decoupled root uses **its own** `-state=...` file (`e2e_tenant.tfstate`, `e2e_servers.tfstate`, `e2e_vpc.tfstate`). They are not one combined Terraform state; together they describe **one tenant** if you keep names and paths aligned.

- **Same tenant, same workflow**: reuse those state paths and always pass the **same** `tenant_name` (and the same `tenant_fabric` on server commands) as in the tenant step. The servers state tracks allocations **for that tenant**—changing `tenant_name` while keeping the old servers state file will confuse Terraform unless you intentionally reset or replace state.
- **A different tenant or a clean slate**: use **new** state filenames under `states/` (or new directories). Do not reuse the same `e2e_*.tfstate` files for another tenant; Terraform would still think the old resources belong to this configuration.
- **VPC peering again**: if Terraform must stop tracking an existing peer (for example you used `delete_on_destroy=false` or you need a fresh apply), run **VPC peering state removal** below so the next `apply` is not blocked by stale state. Skipping that when you expect a new peering run can lead to errors or the wrong object being tracked.

### Tenant creation

```bash
terraform -chdir=/repo/examples/decoupled/01-tenant apply -auto-approve \
  -state=states/e2e_tenant.tfstate \
  -var="tenant_name=tenw01" \
  -var="max_gpus_allowed=8" \
  -var="prefer=respond-sync"
```

### GPU allocation (ADD)

```bash
terraform -chdir=/repo/examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_servers.tfstate \
  -var="tenant_fabric=YOUR_FABRIC_NAME" \
  -var="tenant_name=tenw01" \
  -var="operation=ADD" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=false" \
  -var="prefer=respond-sync"
```

### VPC peering creation

```bash
terraform -chdir=/repo/examples/decoupled/03-vpcpeering apply -auto-approve \
  -state=states/e2e_vpc.tfstate \
  -var="tenant_name=tenw01" \
  -var="vpcpeering_name=tenw01-peer" \
  -var="delete_on_destroy=false"
```

### GPU deallocation (DELETE)

```bash
terraform -chdir=/repo/examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_servers.tfstate \
  -var="tenant_fabric=YOUR_FABRIC_NAME" \
  -var="tenant_name=tenw01" \
  -var="operation=DELETE" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=false" \
  -var="prefer=respond-sync"
```

Tip: To deallocate all servers for a tenant, use an empty list: `-var='servers=[]'`.

### Tenant deletion

```bash
terraform -chdir=/repo/examples/decoupled/01-tenant destroy -auto-approve \
  -state=states/e2e_tenant.tfstate \
  -var="tenant_name=tenw01" \
  -var="prefer=respond-sync"
```

### VPC peering state removal (state-only)

Use this when Terraform must **drop** the peering from state while Fabric still has the peer—often before a **new** peering `apply` with the same root (see **How state files relate to tenants** above).

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

