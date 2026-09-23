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
- **Tenant servers** (`fabricapi_tenant_servers`): allocate/deallocate whole GPU servers (PATCH tenant). `shared=false` dedicates the server's E-W (UFM/NMX-C) GPUs to the tenant (ONES-managed); `shared=true` leaves them free for external assignment via `fabricapi_tenant_gpus`.
- **Per-GPU allocations** (`fabricapi_gpu_allocations`): map/unmap logical GPUs (G0–G7) on servers already attached to a tenant (POST `.../gpuAllocations`)
- **Tenant GPU ports** (`fabricapi_tenant_gpus`): assign/remove GPU ports for a tenant on externally-managed UFM/NMX-C fabrics (POST `.../tenants/{tenant}/gpus`); whole-server (`server_names`) or specific `gpu_ids`, optional UFM `membership`
- **VF assign** (`fabricapi_vf_assign`): bind/unbind HBN VF interfaces to a tenant VLAN (POST/DELETE `.../vf-interfaces/{vfId}/assign`)
- **VPC peering** (`fabricapi_vpcpeering`): create
- **Fabric** (`fabricapi_fabric`): create/delete a fabric via the ONES UI config service (POST `/api/config/addFabricData`, DELETE `/api/config/deletefabricbyname/{name}`) — requires provider `config_endpoint` / `FABRIC_API_CONFIG_ENDPOINT`. `type` is restricted to `"NVIDIA SpX RA 1.3"`, `"NVIDIA SpX RA 2.1"`, or `"Aviz RA 1.0"`. `node_type` (GPU node hardware, e.g. `"gb200"`, `"gb300"`) defaults to `"dgx"` and doesn't need to be set for that case. Supports both east-west (`enable_ew` + `starting_subnet_gpu`) and north-south (`enable_ns` + required `starting_subnet_cpu`, with optional `dedicated_storage`/`starting_subnet_storage`) networking; always ONES-managed tenant control
- **Fabric deploy** (`fabricapi_fabric_deploy`): deploy a `fabricapi_fabric` — uploads real device credentials, SSH-validates switches/servers (hard-fails on any failure), pushes credentials to inventory, pushes generated config to real switches, marks the fabric Deployed; matches the ONES UI's "Deploy Fabric" sequence (`uploadip` → `validateswitch`/`validateserver` → `updateinventory` → `/api/config` → `updatefabricstatus`). UFM/NMX/border-leaf-port flows aren't implemented. No known undeploy API, so destroy only removes it from state. Set `custom_yaml` to deploy a hand-edited or externally-sourced YAML instead of the server's own generated one
- **Inventory sync** (`fabricapi_inventory_sync`): force an immediate UFM inventory reconcile (POST `.../fabrics/{fabric}/inventorySync`)

### Data sources

- **Tenants** (`fabricapi_tenants`): list tenant names for a fabric
- **Available servers** (`fabricapi_available_servers`): list free GPU server hostnames (GET `.../available_servers`)
- **VF interfaces** (`fabricapi_vf_interfaces`): list HBN VF interfaces for a GPU server (GET `.../vf-interfaces`)
- **Fabric YAML** (`fabricapi_fabric_yaml`): fetch a fabric's current generated YAML for review (GET `/fabrics/{name}`) — design-only inspection, no deploy side effects

## Examples (copy/paste friendly)

- **Decoupled roots (recommended)**: `examples/decoupled/`
  - `01-tenant`: tenant lifecycle
  - `02-servers`: whole-server GPU allocation/deallocation
  - `03-vpcpeering`: VPC peering
  - `04-gpu-allocations`: per-GPU allocation/deallocation on shared servers
  - `04-gpus`: external GPU-port assign/remove on UFM/NMX-C fabrics
  - `05-available-servers`: lookup free servers before allocate (read-only)
  - `06-vf-interfaces`: lookup HBN VF interfaces on a server (read-only)
  - `07-vf-assign`: bind/unbind a VF to a tenant VLAN
  - `08-fabric`: create a fabric via the ONES UI config service

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

