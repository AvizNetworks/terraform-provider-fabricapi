# Terraform Provider for Fabric API (`fabricapi`)

Terraform provider for managing Fabric API objects via standard Terraform workflows.

## Choose your workflow

- **Docker-based usage (recommended for most users)**: runs Terraform + provider inside a container.  
  See `README.docker.md`.
- **Local install (Make-based)**: builds and installs the provider into your local Terraform plugin dir.  
  See `README.make.md`.

## What this provider supports

- **Tenants**: create/read/delete
- **Tenant servers (GPUs)**: allocate/deallocate
- **VPC peering**

## Examples (copy/paste friendly)

- **Decoupled roots (recommended)**: `examples/decoupled/`
  - `01-tenant`: tenant lifecycle
  - `02-servers`: GPU allocation/deallocation
  - `03-vpcpeering`: VPC peering
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
|           v1.0.0             |      4.2.1             |


## Quick links

- **Docker guide**: `README.docker.md`
- **Make/local guide**: `README.make.md`
- **Reusable identity file**: `examples/fabric-identity.md`
- **Contributing / development**: `CONTRIBUTING.md`

