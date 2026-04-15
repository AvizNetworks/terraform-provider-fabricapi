# Terraform Provider for Fabric API

This is a custom Terraform provider that manages Fabric API resources including tenants and tenant server assignments.

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
├── Dockerfile                       # Docker build configuration
├── build.sh                         # Build script
└── test.sh                          # Test script
```

## Prerequisites

- Docker installed on your system
- Access to the Fabric API endpoint

## Building the Provider

1. **Clone or create the project structure** with all the files provided above.

2. **Make the build script executable:**
   ```bash
   chmod +x build.sh test.sh
   ```

3. **Build the Docker image:**
   ```bash
   ./build.sh
   ```

This will:
- Build the Go provider binary
- Create a Docker image with Terraform and the custom provider
- Install the provider in the correct plugin directory

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
- `go.mod`, `Dockerfile`, `build.sh`, `test.sh` → root directory

## Testing the Provider

### Docker: common workspace (`/workspace`)

The image sets **`WORKDIR /workspace`**, which is a copy of the repo’s **`examples/`** tree. Start the shell **without** `-w .../01-tenant` so you land in that common root, then `cd` into the Terraform root you want:

```bash
docker run -it --rm \
  -e FABRIC_API_ENDPOINT="https://10.4.5.132:8089" \
  -e FABRIC_NAME="External" \
  -e FABRIC_API_AUTH_ENDPOINT="https://10.4.5.132:8089" \
  -e FABRIC_API_USERNAME="superadmin" \
  -e FABRIC_API_PASSWORD='Admin@1234' \
  -e FABRICAPI_INSECURE_TLS=1 \
  terraform-fabricapi:latest
# prompt shows ...:/workspace#

cd decoupled/01-tenant   # or decoupled/02-servers, decoupled/03-vpcpeering, or stay in /workspace for examples/main.tf
terraform init
terraform apply -auto-approve -var='tenant_name=test1' -var='tenant_description=TF test' -var='max_gpus_allowed=8'
```

Use **`--network host`** only if the API is not reachable from the default Docker bridge (Linux).

### 1. Interactive Testing

Start an interactive shell in the container:
```bash
docker run -it --rm terraform-fabricapi:latest 

terraform init
terraform apply \
  -var="tenant_name=test_tenant" \
  -var='max_gpus_allowed=32' \
  -var='servers=["hgx-su00-h00","hgx-su00-h01","hgx-su00-h02","hgx-su00-h03"]' \
  -var='shared=true' \
  -auto-approve
```

**** Following should not be referred to in this section ****
```bash
docker run -it --rm terraform-fabricapi:latest
```

Inside the container:

```bash
# Initialize Terraform
terraform init

# Validate the configuration
terraform validate
terraform apply -var="tenant_name=test_tenant" -auto-approve

# Preview changes
terraform plan

# Apply changes (only when API is ready)
terraform apply

# Destroy resources
terraform destroy
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

If you want to test without the real API, create a simple mock server:

```bash
# Run a mock API server (in another terminal)
docker run -p 29123:8080 mockserver/mockserver

# Then run your tests
docker run -it --rm --network host terraform-fabricapi:latest terraform plan
```

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
export FABRIC_API_USERNAME="superadmin"
export FABRIC_API_PASSWORD="Admin@1234"

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
2. Check network connectivity from Docker container
3. Use `--network host` flag if running on Linux: 
   ```bash
   docker run -it --rm --network host terraform-fabricapi:latest
   ```

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
2. Run `./build.sh` to rebuild the Docker image
3. Test with `./test.sh` or manually
4. Iterate until desired behavior is achieved

## Notes

- The provider is installed locally, not from a registry
- State is stored locally in the container (use volumes to persist)
- The PATCH endpoint updates global tenant state, not specific tenant
- For production use, consider publishing to Terraform Registry
