# Terraform Provider for Fabric API

Terraform provider (`fabricapi`) for managing Fabric API objects via standard Terraform workflows.

- **Repo**: `https://github.com/AvizNetworks/terraform-provider-fabricapi`

## Key Capabilities

- **Tenant lifecycle management**: create, read, delete tenants
- **GPU allocation**: assign/remove server resources (GPUs) to/from tenants
- **VPC peering**: create VPC peering for tenants
- **Asynchronous workflows**: supports `prefer=respond-async` with optional webhook payloads for non-blocking operations

## Project Structure

```
terraform-provider-fabricapi/
├── main.go                          # Provider entry point
├── internal/
│   └── provider/
│       ├── provider.go              # Provider implementation
│       ├── client.go                # API client
│       ├── tenant_resource.go       # Tenant resource
│       └── tenant_servers_resource.go # Tenant servers resource
├── examples/
│   └── main.tf                      # Example Terraform configuration
├── go.mod                           # Go module definition
├── build.sh                         # Build script
└── test.sh                          # Test script
```

## Prerequisites

- **Local install**: Go (per `go.mod`), Terraform (recommended >= 1.5), `make`
- **Docker workflow**: Docker
- **Always**: network access to your Fabric API endpoint

## Terraform Provider fabricapi (local install)

### Overview

This repository provides a custom Terraform provider, `fabricapi`, designed to manage objects within the Fabric API.
It enables infrastructure-as-code management of Fabric resources using standard Terraform workflows.

### Installation (build and local provider install)

```bash
git clone https://github.com/AvizNetworks/terraform-provider-fabricapi
cd terraform-provider-fabricapi
make install
```

This places the provider binary into Terraform’s local plugin directory so `terraform init` can locate and use it.

### Configure access (required)

Access to the Fabric API is configured primarily via environment variables.

#### Endpoint configuration

- **`FABRIC_API_ENDPOINT`**: Fabric API base URL (example: `http://10.4.5.132:8787`)
- **`FABRIC_NAME`**: Fabric name/ID (example: `Tenant`)

#### Authentication (choose one option)

- **Option A: access token**

```bash
export FABRIC_API_ACCESS_TOKEN="eyJ..."
```

- **Option B: username/password**

```bash
export FABRIC_API_USERNAME="YOUR_USERNAME"
export FABRIC_API_PASSWORD="YOUR_PASSWORD"
# optional if auth is on a different base URL
export FABRIC_API_AUTH_ENDPOINT="https://10.4.5.132:8089"
```

#### TLS configuration (if needed)

If your auth endpoint uses a self-signed certificate (common for testing), you can disable TLS verification:

```bash
export FABRICAPI_INSECURE_TLS=1
```

### Examples layout (decoupled workflow)

- **`examples/decoupled/01-tenant`**: tenant creation and deletion
- **`examples/decoupled/02-servers`**: GPU allocation and deallocation
- **`examples/decoupled/03-vpcpeering`**: VPC peering setup

### One-time initialization (recommended)

Run `terraform init` once for each example root so Terraform can locate the locally installed provider:

```bash
terraform -chdir=./examples/decoupled/01-tenant init -upgrade
terraform -chdir=./examples/decoupled/02-servers init -upgrade
terraform -chdir=./examples/decoupled/03-vpcpeering init -upgrade
```

### End-to-end commands (async + webhooks enabled)

Note: Terraform may show a warning that `-state` is deprecated, but these commands are preserved to match the workflow.

#### 1) Tenant creation

```bash
terraform -chdir=./examples/decoupled/01-tenant apply -auto-approve \
  -state=states/e2e_async_tenant.tfstate \
  -var="tenant_name=tenw01" \
  -var="max_gpus_allowed=8" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var='webhook_url=http://10.4.5.132:8787/test/webhook-receiver' \
  -var='webhook_events=["tenant.create"]'
```

#### 2) GPU allocation (ADD)

```bash
terraform -chdir=./examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_async_servers.tfstate \
  -var="tenant_fabric=Tenant" \
  -var="tenant_name=tenw01" \
  -var="operation=ADD" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=false" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var='webhook_url=http://10.4.5.132:8787/test/webhook-receiver' \
  -var='webhook_events=["tenant.allocate"]'
```

#### 3) VPC peering creation

```bash
terraform -chdir=./examples/decoupled/03-vpcpeering apply -auto-approve \
  -state=states/e2e_vpc.tfstate \
  -var="tenant_name=tenw01" \
  -var="vpcpeering_name=tenw01-peer" \
  -var="delete_on_destroy=false"
```

#### 4) GPU deallocation (DELETE)

```bash
terraform -chdir=./examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_async_servers.tfstate \
  -var="tenant_fabric=Tenant" \
  -var="tenant_name=tenw01" \
  -var="operation=DELETE" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=false" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var='webhook_url=http://10.4.5.132:8787/test/webhook-receiver' \
  -var='webhook_events=["tenant.deallocate"]'
```

Tip: to deallocate all servers for a tenant, use an empty list: `-var='servers=[]'` (avoid `servers=[""]`).

#### 5) Tenant deletion

```bash
terraform -chdir=./examples/decoupled/01-tenant destroy -auto-approve \
  -state=states/e2e_async_tenant.tfstate \
  -var="tenant_name=tenw01" \
  -var="prefer=respond-async" \
  -var="webhooks_enabled=true" \
  -var='webhook_url=http://10.4.5.132:8787/test/webhook-receiver' \
  -var='webhook_events=["tenant.delete"]'
```

#### 6) VPC peering state removal (state-only)

If you only want Terraform to forget the VPC peering object (leaving it in the API):

```bash
terraform -chdir=./examples/decoupled/03-vpcpeering state rm \
  -state=states/e2e_vpc.tfstate \
  fabricapi_vpcpeering.this
```

### Troubleshooting

- **Provider not found**: run `make install` again, then `terraform init -upgrade` in the relevant example folder
- **Async + webhook errors**: when using `prefer=respond-async` and `webhooks_enabled=true`, you must set both `webhook_url` and `webhook_events`
- **Connectivity failures**: verify `FABRIC_API_ENDPOINT` and ensure your machine can reach the endpoint

## Terraform Provider fabricapi (Docker workflow)

### Overview

This workflow runs Terraform and the provider **inside a container**. It is independent of the local install workflow.

### Build the Docker image

```bash
./docker-build.sh
```

### Run (repo-mounted “old workflow”)

This is the same pattern you previously used (`/repo` mount). Run the container:

```bash
sudo docker run -it --rm \
  -e FABRIC_API_ENDPOINT="https://10.4.5.132:8089" \
  -e FABRIC_API_AUTH_ENDPOINT="https://10.4.5.132:8089" \
  -e FABRIC_NAME="Tenant" \
  -e FABRIC_API_USERNAME="YOUR_USERNAME" \
  -e FABRIC_API_PASSWORD="YOUR_PASSWORD" \
  -v "$PWD:/repo" \
  terraform-fabricapi:latest
```

If you are testing against a **local** endpoint and you truly need host networking, add `--network host`.

If your auth endpoint uses a self-signed certificate (common in lab/testing), you can set `FABRICAPI_INSECURE_TLS=1` to disable TLS verification. Avoid using this in production.

Inside the container, run the same init/apply/destroy commands, but with `/repo/...` paths:

```bash
terraform -chdir=/repo/examples/decoupled/01-tenant init -upgrade
terraform -chdir=/repo/examples/decoupled/02-servers init -upgrade
terraform -chdir=/repo/examples/decoupled/03-vpcpeering init -upgrade
```

E2E commands are identical to the local workflow above; only the paths change from `./examples/...` to `/repo/examples/...`.

## Project Setup

Create the following directory structure:

```
mkdir -p terraform-provider-fabricapi/internal/provider
mkdir -p terraform-provider-fabricapi/examples
```

Place the files as follows:
- `main.go` → root directory
- `provider.go`, `client.go`, `tenant_resource.go`, `tenant_servers_resource.go` → `internal/provider/`
- `main.tf` → `examples/`
- `go.mod`, `build.sh`, `test.sh` → root directory

## Testing the Provider

### Local: run examples directly

After `./build.sh`, run Terraform in the example root(s) under `examples/`.

### 1. Interactive Testing

```bash
cd examples
terraform init
terraform apply -auto-approve \
  -var="tenant_name=test_tenant" \
  -var='max_gpus_allowed=32' \
  -var='servers=["hgx-su00-h00","hgx-su00-h01","hgx-su00-h02","hgx-su00-h03"]' \
  -var='shared=true'
```

### 2. Automated Testing

Run the test script:

```bash
./test.sh
```

This will run through initialization, validation, and planning steps.

### 3. Testing Against Real API

When you're ready to test against the actual API:

1. Update the endpoint in `examples/main.tf` if needed
2. Uncomment the apply and destroy commands in `test.sh`
3. Run: `./test.sh`

### 4. Mock API Testing

If you want to test without the real API, point `endpoint` to a mock server that implements the Fabric API endpoints used by the provider.

## Using the Provider

### Provider Configuration

```hcl
provider "fabricapi" {
  endpoint = "http://worker07.air.nvidia.com:29123"  # API endpoint
  fabric   = "fab"                                    # Fabric name
}
```

You can also use environment variables:
```bash
export FABRIC_API_ENDPOINT="http://worker07.air.nvidia.com:29123"
export FABRIC_NAME="fab"
```

#### Authentication (JWT)

The provider supports these authentication modes:

1. **`access_token` (optional)**: supply a JWT access token yourself; the provider skips `POST /login` and sends it as `Authorization: Bearer …`. Combine with `refresh_token` if you still want automatic refresh on `401`.
2. **`username` + `password`**: the provider calls `POST /login` to fetch `access_token` + `refresh_token` when no `access_token` is set.

If an API call returns 401 (expired access token), the provider will automatically call `POST /refresh` (using the stored `refresh_token`) once and retry the original request. If the refresh token is expired/invalid, the run will fail and you must login again.

To revoke the refresh token on the server (Spring-style `POST /api/auth/logout` with body `{"refresh_token":"..."}`), add a dedicated resource **after** your other resources (or apply it in a separate run so in-flight API calls are not affected):

```hcl
resource "fabricapi_auth_logout" "revoke" {}
```

The logout URL is `{auth_endpoint}/api/auth/logout` (same base as login; defaults to `endpoint` when `auth_endpoint` is unset).

Configure via provider attributes:

```hcl
provider "fabricapi" {
  endpoint      = "http://10.4.5.76:8787"
  fabric        = "8407"

  # Option A: bring your own access token
  # access_token = var.fabricapi_access_token

  # Option B: generate tokens via login
  # auth_endpoint = "https://localhost:8089" # defaults to endpoint if unset
  # username      = var.fabricapi_username
  # password      = var.fabricapi_password
}
```

Or via environment variables:

```bash
export FABRIC_API_ENDPOINT="http://10.4.5.76:8787"
export FABRIC_NAME="8407"

# Option A: access token you obtained out-of-band
# export FABRIC_API_ACCESS_TOKEN="eyJ..."

# Option B: login to fetch tokens
export FABRIC_API_AUTH_ENDPOINT="https://localhost:8089"  # optional; defaults to FABRIC_API_ENDPOINT
export FABRIC_API_USERNAME="YOUR_USERNAME"
export FABRIC_API_PASSWORD="YOUR_PASSWORD"

# Optional: if you want to supply refresh token explicitly (otherwise it is learned from /login)
export FABRIC_API_REFRESH_TOKEN="..."
```

If your auth endpoint requires TLS (common on `:8089`) and you are using a self-signed certificate for testing, set:

```bash
export FABRICAPI_INSECURE_TLS=1
```

### Creating a Tenant

```hcl
resource "fabricapi_tenant" "example" {
  tenant_name      = "tenant2"
  description      = "Test tenant for GPU workloads"
  max_gpus_allowed = 32 # 8, 16, 24, or 32 — must be >= len(servers) * 8

  # Optional async/webhook controls (default is synchronous)
  # prefer           = "respond-sync"  # or "respond-async" (underscore forms also accepted)
  # webhooks_enabled = false
  # webhook_url      = "http://localhost:8787/test/webhook-receiver"
  # webhook_events   = ["tenant.create"]
}
```

### Managing Tenant Servers

```hcl
resource "fabricapi_tenant_servers" "add_servers" {
  # Tenant must already exist. Use tenant resource reference (implicit dependency)
  # or explicit depends_on for sequencing.
  # Optional: override the fabric used in the backend URL
  # /fabrics/{fabric}/tenants/{tenant}. If unset, uses provider-level fabric.
  # fabric_name = "1SU-Fabric170619"
  tenant_name = fabricapi_tenant.example.tenant_name
  operation = "ADD"
  shared    = true
  # Up to 4 servers (32 GPUs max); list every host you want GPUs from
  servers = [
    "hgx-su00-h00",
    "hgx-su00-h01",
    "hgx-su00-h02",
    "hgx-su00-h03",
  ]

  depends_on = [fabricapi_tenant.example]

  # Optional async/webhook controls (default is synchronous)
  # prefer           = "respond-sync"  # or "respond-async" (underscore forms also accepted)
  # webhooks_enabled = false
  # webhook_url      = "http://localhost:8787/test/webhook-receiver"
  # webhook_events   = ["tenant.allocate", "tenant.deallocate"]
}

resource "fabricapi_tenant_servers" "remove_servers" {
  operation = "REMOVE"
  shared    = false
  servers   = [
    "hgx-su00-h00"
  ]
}
```

#### Async + webhooks behavior

Applies to **`fabricapi_tenant`**, **`fabricapi_tenant_servers`**, and **`fabricapi_vpcpeering`** (same rules).

- **Prefer (default)**: `prefer = "respond-sync"` (HTTP `Prefer: respond-sync`) — provider waits for completion. Values `respond_sync` / `respond_async` are still accepted and normalized to hyphen form for the API.
- **Async without webhooks**: set `prefer = "respond-async"` and `webhooks_enabled = false`. The provider will poll `GET /operations/{operationId}` until the job completes, then continue.
- **Async with webhooks**: set `prefer = "respond-async"` and `webhooks_enabled = true`. You must also set `webhook_url` and `webhook_events`. The provider will **not** poll the operation; the backend will notify your webhook receiver. The async job id is stored in the computed attribute `operation_id`.

`fabricapi_tenant_servers` is PATCH-only and operates on an existing tenant.
On updates, the provider compares current allocated servers vs desired `servers` and issues
the required `ADD` / `DELETE` PATCH calls.

#### Important: `servers` is the source of truth on updates

- **Updates are declarative**: the `servers` list represents the desired final allocation.
  To deallocate one server, remove it from `servers` and run `terraform apply`.
- **Changing only `operation` does not force a deallocation/allocation on update**.
  If `servers` does not change, Terraform has nothing to reconcile and no PATCH call will be sent.
  If you want a pure "do DELETE now" workflow, use a separate resource instance or `terraform destroy -target=...`.

### Two-step apply pattern

You can apply in two explicit phases:

```bash
# Step 1: tenant only
terraform apply -target=fabricapi_tenant.example

# Step 2: server mapping for existing tenant
terraform apply -target=fabricapi_tenant_servers.add_servers
```

With normal dependencies (`tenant_name` reference or `depends_on`), a single
`terraform apply` also sequences tenant first, then tenant_servers.

### Destroy ordering

You can destroy `fabricapi_tenant_servers` explicitly before `fabricapi_tenant` (recommended for clear intent).

If a tenant delete is requested while GPUs are still allocated, the tenant delete flow performs a backend check
(`GET /fabrics/{fabric}/tenants/{tenant}`) and deallocates allocated servers first, then deletes the tenant.
This check is based on live API data (not Terraform state), so both workflows are supported:

- explicit two-step deallocate then delete
- direct tenant delete with automatic pre-delete deallocation

#### Tenant deletion inputs

`terraform destroy` should only require `tenant_name` (and optionally `fabric_name` if you override it on the resource).
`description` and `max_gpus_allowed` are required for create, but are not required to be re-specified for destroy.

## Reusing API endpoint, fabric, and tenant name

Set `api_endpoint`, `fabric_name`, and `tenant_name` once in `examples/fabric.identity.auto.tfvars` (copy from `fabric.identity.auto.tfvars.example`). Terraform auto-loads `*.auto.tfvars`, so later `terraform apply` / `destroy` commands do not need those three `-var=...` flags. Override any single run with `-var='api_endpoint=...'` (CLI wins). Details: `examples/fabric-identity.md`.

## Decoupling apply into 3 commands

The original `examples/main.tf` chains tenant creation → GPU allocation → VPC peering in a single `terraform apply`.

If you want three separate commands/applies (one per action), use:

- `examples/decoupled/01-tenant`: create tenant
- `examples/decoupled/02-servers`: allocate/deallocate GPUs (tenant servers)
- `examples/decoupled/03-vpcpeering`: create VPC peering

See `examples/decoupled/README.md` for the exact commands.

## Troubleshooting

### Provider Not Found

If you get "provider not found" errors:
1. Verify the provider binary is in the correct location
2. Check the provider source matches: `local/fabricapi`
3. Run `terraform init -upgrade`

### API Connection Issues

If you can't connect to the API:
1. Verify the endpoint URL is correct
2. Check network connectivity from your host (e.g. `curl` the API health/login endpoint)

### Build Errors

If the build fails:
1. Ensure you have all source files in the correct directories
2. Run `go mod tidy` to resolve dependencies
3. Check Go version compatibility (needs Go 1.21+)

## Customization

### Adding More Resources

To add support for your third API endpoint:

1. Define the resource struct in a new file (e.g., `new_resource.go`)
2. Implement the Resource interface methods (CRUD operations)
3. Add the client methods to `client.go`
4. Register the resource in `provider.go`'s `Resources()` method

### Modifying Configuration

Edit `examples/main.tf` to change:
- Tenant names and properties
- Server lists
- Provider configuration

## Development Workflow

1. Make changes to Go source files
2. Run `./build.sh` to rebuild + reinstall the local provider
3. Test with `./test.sh` or manually
4. Iterate until desired behavior is achieved

## Notes

- The provider is installed locally, not from a registry
- State is stored locally on your machine (Terraform state files under each example root)
- The PATCH endpoint updates global tenant state, not specific tenant
- For production use, consider publishing to Terraform Registry
