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
- **Async workflows**: `prefer=respond-async` with optional webhooks

## Examples (copy/paste friendly)

- **Decoupled roots (recommended)**: `examples/decoupled/`
  - `01-tenant`: tenant lifecycle
  - `02-servers`: GPU allocation/deallocation
  - `03-vpcpeering`: VPC peering

Start here for exact commands: `examples/decoupled/README.md`.

## Quick links

- **Docker guide**: `README.docker.md`
- **Make/local guide**: `README.make.md`
- **Reusable identity file**: `examples/fabric-identity.md`
- **Contributing / development**: `CONTRIBUTING.md`
