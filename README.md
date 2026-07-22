# Terraform Provider for Fabric API (`fabricapi`)

Terraform provider for managing Fabric API objects via standard Terraform workflows.

## Choose your workflow

- **Docker-based usage (recommended for most users)**: runs Terraform + provider inside a container.  
  See `README.docker.md`.
- **Local install (Make-based)**: builds and installs the provider into your local Terraform plugin dir.  
  See `README.make.md`.

## What this provider supports

### Resources

- **Tenants** (`fabricapi_tenant`): create/read/delete
- **Tenant servers** (`fabricapi_tenant_servers`): allocate/deallocate whole GPU servers (PATCH tenant)
- **Per-GPU allocations** (`fabricapi_gpu_allocations`): map/unmap logical GPUs (G0–G7) on servers already attached to a tenant (POST `.../gpuAllocations`)
- **VPC peering** (`fabricapi_vpcpeering`): create

### Data sources

- **Tenants** (`fabricapi_tenants`): list tenant names for a fabric
- **Available servers** (`fabricapi_available_servers`): list free GPU server hostnames (GET `.../available_servers`)

## Examples (copy/paste friendly)

- **Decoupled roots (recommended)**: `examples/decoupled/`
  - `01-tenant`: tenant lifecycle
  - `02-servers`: whole-server GPU allocation/deallocation
  - `03-vpcpeering`: VPC peering
  - `04-gpu-allocations`: per-GPU allocation/deallocation on shared servers
  - `05-available-servers`: lookup free servers before allocate (read-only)
- **State files**: each root keeps its own state; use one consistent `tenant_name` across those commands for the same tenant, new state filenames for a different tenant, and follow the guides for VPC peering cleanup. See **How state files relate to tenants** in `README.docker.md` or `README.make.md`.

Start here for exact commands: `examples/decoupled/README.md`.

## Versioning Policy

This repository's version follows [Semantic Versioning (SemVer)](https://semver.org/) (`MAJOR.MINOR.PATCH`) and is maintained **independently** of AVIZ ONES Spectrum-X platform releases — a version bump here does not imply a corresponding change in the ONES platform version, and vice versa.
- MAJOR: Incremented for breaking, backward-incompatible changes (e.g., 2.0.0)
- MINOR: Incremented when adding new, backward-compatible features or functionality (e.g., 2.1.0)
- PATCH: Incremented for backward-compatible bug fixes and small corrections (e.g., 2.1.1)

### Compatibility Matrix

| Terraform-provider-fabricapi | Supported ONES Version |
|------------------------------|---------------------   |
|           v1.1.0             |      4.2.1             |


## Quick links

- **Docker guide**: `README.docker.md`
- **Make/local guide**: `README.make.md`
- **Reusable identity file**: `examples/fabric-identity.md`
- **Contributing / development**: `CONTRIBUTING.md`

