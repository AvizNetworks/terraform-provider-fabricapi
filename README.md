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

### 1. Interactive Testing

Start an interactive shell in the container:A
```bash
docker run -it --rm terraform-fabricapi:latest 

terraform init
terraform apply \
  -var="tenant_name=test_tenant" \
  -var='servers=["hgx-su00-h00","hgx-su00-h01"]' \
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

### Creating a Tenant

```hcl
resource "fabricapi_tenant" "example" {
  tenant_name      = "tenant2"
  description      = "Test tenant for GPU workloads"
  max_gpus_allowed = 16
}
```

### Managing Tenant Servers

```hcl
resource "fabricapi_tenant_servers" "add_servers" {
  operation = "ADD"
  servers   = [
    "hgx-su00-h00",
    "hgx-su00-h01"
  ]
  
  depends_on = [fabricapi_tenant.example]
}

resource "fabricapi_tenant_servers" "remove_servers" {
  operation = "REMOVE"
  servers   = [
    "hgx-su00-h00"
  ]
}
```

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
