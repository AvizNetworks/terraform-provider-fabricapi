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
- `FABRIC_NAME`: Fabric name/ID (example: `fabric01`)
- `FABRIC_API_CONFIG_ENDPOINT`: ONES UI config service base URL, used by both `fabricapi_fabric` (POST `/api/config/addFabricData`) and `fabricapi_fabric_deploy` (uploadip/validateswitch/validateserver/updateinventory/api/config/updatefabricstatus). This is a **separate host/port** from `FABRIC_API_ENDPOINT` (example: `https://localhost`).

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

### TLS (only for lab/testing)

If your auth endpoint uses a self-signed certificate, you can disable TLS verification:

```bash
export FABRICAPI_INSECURE_TLS=1
```

Avoid using this in production.

## Recommended examples workflow (decoupled)

The most copy/paste friendly workflow is under `examples/decoupled/` where each action is a separate Terraform root:

- `examples/decoupled/01-tenant` — tenant lifecycle
- `examples/decoupled/02-servers` — whole-server allocate/deallocate (PATCH tenant)
- `examples/decoupled/03-vpcpeering` — VPC peering
- `examples/decoupled/04-gpu-allocations` — per-GPU ADD/DELETE (POST `gpuAllocations`; externally managed fabrics)
- `examples/decoupled/04-gpus` — external GPU-port ADD/DELETE on UFM/NMX-C fabrics (POST `/gpus`)
- `examples/decoupled/05-available-servers` — free server lookup (GET `available_servers`)
- `examples/decoupled/06-vf-interfaces` — HBN VF lookup (GET `vf-interfaces`)
- `examples/decoupled/07-vf-assign` — HBN VF bind/unbind (POST/DELETE `vf-interfaces/{vfId}/assign`)

### One-time init (per root)

```bash
terraform -chdir=examples/decoupled/01-tenant init -upgrade
terraform -chdir=examples/decoupled/02-servers init -upgrade
terraform -chdir=examples/decoupled/03-vpcpeering init -upgrade
terraform -chdir=examples/decoupled/04-gpu-allocations init -upgrade
terraform -chdir=examples/decoupled/04-gpus init -upgrade
terraform -chdir=examples/decoupled/05-available-servers init -upgrade
terraform -chdir=examples/decoupled/06-vf-interfaces init -upgrade
terraform -chdir=examples/decoupled/07-vf-assign init -upgrade
```

## End-to-end commands

The following commands demonstrate a full end-to-end workflow. The examples use `prefer=respond-sync` (you can omit `prefer` where the defaults match).

Note: Terraform may show a warning that `-state` is deprecated, but these commands are preserved to match the workflow.

**Example values (external / shared GPU fabric):** **`Get_fab`**, **`tenant1`**, **`hgx-su00-h00`**.  
**Example values (HBN / DPU fabric):** **`HBN_test_16`**, **`Blue`**, **`hgx-su00-h01`**, **`vf4`**.  
Substitute real names from `GET /fabrics`, `05-available-servers`, and `06-vf-interfaces` output.

### 0) Configure connectivity (required)

```bash
export FABRIC_API_ENDPOINT="https://10.4.5.132:8089"
export FABRIC_NAME="Get_fab"
export FABRIC_API_USERNAME="superadmin"
export FABRIC_API_PASSWORD="YOUR_PASSWORD"
export FABRICAPI_INSECURE_TLS=1
```

For HBN testing, set `FABRIC_NAME` to your HBN fabric (e.g. `HBN_test_16`) instead.

### State directories (one-time)

Create the local `states/` directories under each decoupled example **before** the first `apply` that uses those paths.

Run these `mkdir` commands only the **first** time you use the provider with these examples, or if those directories (or their parent trees) were removed. You do **not** need to run them again for routine applies or destroys.

```bash
mkdir -p ./examples/decoupled/01-tenant/states
mkdir -p ./examples/decoupled/02-servers/states
mkdir -p ./examples/decoupled/03-vpcpeering/states
mkdir -p ./examples/decoupled/04-gpu-allocations/states
mkdir -p ./examples/decoupled/04-gpus/states
mkdir -p ./examples/decoupled/05-available-servers/states
mkdir -p ./examples/decoupled/06-vf-interfaces/states
mkdir -p ./examples/decoupled/07-vf-assign/states
```

### How state files relate to tenants

Each decoupled root uses **its own** `-state=...` file. Together they describe **one tenant** when names and paths stay aligned.

| Root | Typical state file | Notes |
|------|-------------------|-------|
| `01-tenant` | `e2e_tenant.tfstate` | Managed resource |
| `02-servers` | `e2e_servers.tfstate` | Whole-server allocate/deallocate |
| `04-gpu-allocations` | `e2e_gpu_alloc.tfstate` | Per-GPU ADD/DELETE (external fabrics) |
| `04-gpus` | `e2e_tenant_gpus.tfstate` | External GPU-port ADD/DELETE (UFM/NMX-C)|
| `05-available-servers` | `e2e_available_servers.tfstate` | Read-only lookup; refreshed each apply |
| `06-vf-interfaces` | `e2e_vf_interfaces.tfstate` | Read-only HBN VF lookup; refreshed each apply |
| `07-vf-assign` | `e2e_vf_assign.tfstate` | HBN VF assign; destroy to unbind |
| `03-vpcpeering` | `e2e_vpc.tfstate` | VPC peering |

- **Same tenant, same workflow**: reuse state paths and the same `tenant_name` / `tenant_fabric`.
- **New tenant or clean slate**: use new state filenames under `states/`.
- **Available servers / VF interfaces**: no destroy or `state rm` needed — re-apply to refresh the list.
- **VPC peering again**: see **VPC peering state removal** below if stale state blocks a new peering run.

### 1) Tenant creation

```bash
terraform -chdir=./examples/decoupled/01-tenant apply -auto-approve \
  -state=states/e2e_tenant.tfstate \
  -var="tenant_name=tenant1" \
  -var="tenant_description=TF Get_fab test" \
  -var="max_gpus_allowed=8" \
  -var="prefer=respond-sync"
```

### Available servers (lookup) — GET `available_servers`

Read-only data source (`fabricapi_available_servers`) — lists free GPU server hostnames for a fabric.

- Run **before** whole-server allocate to discover a hostname.
- Does **not** create or delete anything on the fabric.
- Same state file can be reused forever; each `apply` refreshes the list.
- No `destroy` or `terraform state rm` needed.

```bash
terraform -chdir=./examples/decoupled/05-available-servers apply -auto-approve \
  -state=states/e2e_available_servers.tfstate \
  -var="fabric_name=Get_fab"
```

Use a hostname from the `available_gpus` output in the next step.

### 2) GPU allocation — whole server (ADD)

```bash
terraform -chdir=./examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_servers.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=ADD" \
  -var='servers=["hgx-su00-h00"]' \
  -var="shared=true" \
  -var="prefer=respond-sync"
```

Set `shared=true` for shared / per-GPU and HBN flows.

### Per-GPU allocation (ADD) — POST `gpuAllocations`

Maps individual GPUs (e.g. G6, G7) on servers **already attached** to the tenant.

**Prerequisites:**

1. Tenant exists (`01-tenant`).
2. Server attached via `02-servers` ADD.
3. Fabric is **externally managed** (`is_ones_controlled=false`). ONES-controlled fabrics reject `gpuAllocations`.

```bash
terraform -chdir=./examples/decoupled/04-gpu-allocations apply -auto-approve \
  -state=states/e2e_gpu_alloc.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=ADD" \
  -var='allocations=[{suid=0,server="hgx-su00-h00",gpus=["G6","G7"]}]' \
  -var="prefer=respond-sync"
```

### Per-GPU deallocation (DELETE) — POST `gpuAllocations`

Same resource and state file; only `operation` changes. Keeps the resource in Terraform state (day-2 reuse). Use `destroy` only when removing Terraform management entirely.

```bash
terraform -chdir=./examples/decoupled/04-gpu-allocations apply -auto-approve \
  -state=states/e2e_gpu_alloc.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=DELETE" \
  -var='allocations=[{suid=0,server="hgx-su00-h00",gpus=["G6","G7"]}]' \
  -var="prefer=respond-sync"
```

### HBN — VF interfaces (lookup) — GET `vf-interfaces`

Read-only data source (`fabricapi_vf_interfaces`) — lists DPU VF interfaces for a GPU server.

- Same pattern as available servers: re-apply to refresh; no destroy needed.
- Pick a VF with `status=free` before assign (`vf_id` may be `server_if` like `vf4` or DPU `if_name`).

```bash
terraform -chdir=./examples/decoupled/06-vf-interfaces apply -auto-approve \
  -state=states/e2e_vf_interfaces.tfstate \
  -var="fabric_name=HBN_test_16" \
  -var="server_name=hgx-su00-h01"
```

### HBN — VF assign (bind) — POST `.../assign`

Binds a VF to the tenant VLAN. Body includes `tenantName` (required).

**Prerequisites:** tenant exists, server is attached to the tenant, fabric is DPU/HBN offload, VF is free.

```bash
terraform -chdir=./examples/decoupled/07-vf-assign apply -auto-approve \
  -state=states/e2e_vf_assign.tfstate \
  -var="fabric_name=HBN_test_16" \
  -var="server_name=hgx-su00-h01" \
  -var="vf_id=vf4" \
  -var="tenant_name=Blue" \
  -var="prefer=respond-sync"
```

### HBN — VF unbind — DELETE `.../assign`

Destroy sends `{"tenantName":"..."}` in the body (matches the Fabric API sample).

```bash
terraform -chdir=./examples/decoupled/07-vf-assign destroy -auto-approve \
  -state=states/e2e_vf_assign.tfstate \
  -var="fabric_name=HBN_test_16" \
  -var="server_name=hgx-su00-h01" \
  -var="vf_id=vf4" \
  -var="tenant_name=Blue" \
  -var="prefer=respond-sync"
```

### 3) VPC peering creation

```bash
terraform -chdir=./examples/decoupled/03-vpcpeering apply -auto-approve \
  -state=states/e2e_vpc.tfstate \
  -var="tenant_name=tenant1" \
  -var="vpcpeering_name=tf-vpcpeering-tenant1" \
  -var="delete_on_destroy=false"
```

Fabric comes from `FABRIC_NAME` / provider `fabric`.

### 4) GPU deallocation — whole server (DELETE)

```bash
terraform -chdir=./examples/decoupled/02-servers apply -auto-approve \
  -state=states/e2e_servers.tfstate \
  -var="tenant_fabric=Get_fab" \
  -var="tenant_name=tenant1" \
  -var="operation=DELETE" \
  -var='servers=["hgx-su00-h00"]' \
  -var="prefer=respond-sync"
```

Tip: To deallocate all servers for a tenant, use an empty list: `-var='servers=[]'`. Avoid using `servers=[""]`.

### 5) Tenant deletion

Unbind VFs and deallocate servers (and per-GPU mappings if used) before destroy when the API requires an empty tenant.

```bash
terraform -chdir=./examples/decoupled/01-tenant destroy -auto-approve \
  -state=states/e2e_tenant.tfstate \
  -var="tenant_name=tenant1" \
  -var="tenant_description=TF Get_fab test" \
  -var="max_gpus_allowed=8" \
  -var="prefer=respond-sync"
```

### 6) VPC peering state removal (state-only)

Use this command to instruct Terraform to forget the VPC peering object in its state file, leaving the resource intact in the Fabric API. You typically need this when you want Terraform to **manage peering again** from a clean slate (see **How state files relate to tenants** above).

```bash
terraform -chdir=./examples/decoupled/03-vpcpeering state rm \
  -state=states/e2e_vpc.tfstate \
  fabricapi_vpcpeering.this
```

## 08 - Fabric (design + deploy via ONES UI config service)

`examples/decoupled/08-fabric` manages two resources that mirror the ONES UI's own "Design" and
"Deploy Fabric" actions:

- `fabricapi_fabric` — POST `/api/config/addFabricData` to create a Draft fabric (generates a
  skeleton YAML + inventory with blank device credentials). `destroy` **always** calls DELETE
  `/api/config/deletefabricbyname/{name}` — this genuinely removes the fabric from ONES, not
  just from Terraform state.
- `fabricapi_fabric_yaml` (data source) — GET `/fabrics/{name}`, fetched on every `apply`
  regardless of `deploy`. Read it with `terraform output -raw generated_yaml`. **Note:** once a
  prior apply has deployed the fabric (`uploadip` has run), this fetch returns the server's
  credential-filled YAML, not the blank-credential Draft — `local_file.generated_yaml` below is
  owner-only (`0600`/`0700`) for exactly this reason.
- `fabricapi_fabric_deploy` — the "Deploy Fabric" button's full sequence, opt-in via
  `var.deploy=true`:
  1. `uploadip` — fills in real `ip`/`username`/`password` per device.
  2. `validateswitch` — SSH-validates every switch in `devices`/`devices_file` with
     `apply_config=true`. **Hard-fails the apply** if any switch doesn't report a build. Only
     runs when at least one switch device is present.
  3. `validateserver` — SSH-validates every server/host device. **Hard-fails the apply** if any
     server doesn't report an OS. Only runs when at least one server device is present.
  4. `updateinventory` — pushes device credentials to the downstream FM engine's inventory.
  5. Pushes config to the real switches — either `custom_yaml`/`custom_yaml_path` if set, or the
     fabric's own current generated YAML (step 1's output) otherwise.
  6. `updatefabricstatus` — marks the fabric `Deployed`.

  Steps 2 and 3 produce **no log output on success** — only `uploadip`, `updateinventory`, the
  config push, and `updatefabricstatus` print a Terraform warning when they succeed, so a quiet
  apply for the validate steps means they passed, not that they were skipped. On failure they are
  a **hard error** (`terraform apply` aborts), not a warning — don't expect a deploy to complete
  with a validation failure merely logged. `destroy` on this resource is **state-only**: there is
  no known "undeploy" API, so the fabric stays `Deployed` in ONES after `terraform destroy`
  removes it from Terraform's management.

Requires `FABRIC_API_CONFIG_ENDPOINT` (provider `config_endpoint` or env) — a **separate
host/port** from `FABRIC_API_ENDPOINT` (the ONES UI/config backend, not the Fabric API).

### `devices` / `devices_file`

`var.devices` (or `var.devices_file`, a JSON file — see
`examples/decoupled/08-fabric/devices.json.example`) must cover every device in the fabric's
generated inventory, each with `hostname`/`ip`/`username`/`password` and optional
`device_type`/`device_role`/`apply_config` (default `true`). `devices_file` takes precedence over
`devices` when both are set. Real credential files matching `**/devices.json` are gitignored —
only the `.example` is tracked; never commit real device credentials.

### `custom_yaml_path`

Optional (default unset). When omitted, `deploy=true` pushes the fabric's own current generated
YAML as-is. Set it to deploy a hand-edited or externally-sourced YAML instead — see
`examples/decoupled/08-fabric/scenarios-ewns.md` for the four usage scenarios (deploy in one call,
design-only, design→review→edit→deploy, deploy a YAML from elsewhere).

### Sample commands — design only, view the YAML (no deploy)

```bash
mkdir -p ./examples/decoupled/08-fabric/states
terraform -chdir=./examples/decoupled/08-fabric init -upgrade

export FABRIC_API_CONFIG_ENDPOINT="https://localhost"

terraform -chdir=./examples/decoupled/08-fabric apply -auto-approve \
  -state=states/e2e_fabric.tfstate \
  -var="name=fabric-ew-only-customyaml" -var="type=Aviz RA 1.0" -var="description=ew only" \
  -var='su_config={number_of_sus=1,max_number_of_sus=1,hosts_per_su="{0:2}",tenant_ctrl="ones"}' \
  -var='network_config={starting_subnet_gpu="172",enable_east_west_networking=true}' \
  -var="deploy=false"

terraform -chdir=./examples/decoupled/08-fabric output \
  -state=states/e2e_fabric.tfstate \
  -raw generated_yaml > examples/decoupled/08-fabric/fabric.review.yaml
```

Note the `output` flag order: `-state=...` must come **before** `-raw generated_yaml`, not after
— Terraform treats anything after the first positional argument (`generated_yaml`) as another
positional argument, not a flag, and errors with "Unexpected argument".

### Sample commands — deploy in one call

```bash
terraform -chdir=./examples/decoupled/08-fabric apply -auto-approve \
  -state=states/e2e_fabric.tfstate \
  -var="name=fabric-ew-only-customyaml" -var="type=Aviz RA 1.0" -var="description=ew only" \
  -var='su_config={number_of_sus=1,max_number_of_sus=1,hosts_per_su="{0:2}",tenant_ctrl="ones"}' \
  -var='network_config={starting_subnet_gpu="172",enable_east_west_networking=true}' \
  -var="deploy=true" \
  -var="devices_file=/absolute/path/to/devices.json"
```

### Sample commands — destroy (deletes the fabric for real)

Use the same `-var` values as whichever `apply` created the state:

```bash
terraform -chdir=./examples/decoupled/08-fabric destroy -auto-approve \
  -state=states/e2e_fabric.tfstate \
  -var="name=fabric-ew-only-customyaml" -var="type=Aviz RA 1.0" -var="description=ew only" \
  -var='su_config={number_of_sus=1,max_number_of_sus=1,hosts_per_su="{0:2}",tenant_ctrl="ones"}' \
  -var='network_config={starting_subnet_gpu="172",enable_east_west_networking=true}' \
  -var="deploy=true" \
  -var="devices_file=/absolute/path/to/devices.json"
```

### End-to-end: fabric → tenant → server

Once the fabric above is deployed, point `FABRIC_NAME` at it and continue with the tenant and
server flows from earlier in this guide — e.g. **1) Tenant creation** and **2) GPU allocation —
whole server (ADD)** — substituting `fabric-ew-only-customyaml` for `Get_fab` and a real hostname
from that fabric's inventory (e.g. `hgx-su00-h00`) for `servers`. Deallocate with **4) GPU
deallocation — whole server (DELETE)** and delete the tenant with **5) Tenant deletion** before
destroying the fabric itself, if the API requires an empty tenant first.

## Alternative examples

- `examples/` (single Terraform root): `examples/main.tf`
- Identity reuse: `examples/fabric-identity.md` (create `examples/fabric.identity.auto.tfvars` once and avoid repeating identity flags)

## Troubleshooting

- **Provider not found**: re-run `make install`, then re-run `terraform init -upgrade` in the example root.
- **Checksum mismatch / lock file issues**: delete `.terraform.lock.hcl` in that root and re-run `terraform init`.
- **GOOS/GOARCH not detected**: ensure `go` is in `PATH` (`go version` should work).
- **Connectivity failures**: verify `FABRIC_API_ENDPOINT` and ensure your machine can reach the endpoint.
- **403 LICENSED**: devices in the fabric are not licensed. Lab-only: override via `ones-fm-db` on the FM host, then retry.
- **HBN assign `SERVER_NOT_IN_TENANT`**: attach the server with `02-servers` ADD first.

### Lock file cleanup commands (only if you rebuilt the provider)

Usually you do **not** need to delete `.terraform.lock.hcl`. Only do this if Terraform reports a provider checksum mismatch after rebuilding the provider binary:

```bash
rm -f ./examples/decoupled/01-tenant/.terraform.lock.hcl
rm -f ./examples/decoupled/02-servers/.terraform.lock.hcl
rm -f ./examples/decoupled/03-vpcpeering/.terraform.lock.hcl
rm -f ./examples/decoupled/04-gpu-allocations/.terraform.lock.hcl
rm -f ./examples/decoupled/04-gpus/.terraform.lock.hcl
rm -f ./examples/decoupled/05-available-servers/.terraform.lock.hcl
rm -f ./examples/decoupled/06-vf-interfaces/.terraform.lock.hcl
rm -f ./examples/decoupled/07-vf-assign/.terraform.lock.hcl

terraform -chdir=./examples/decoupled/01-tenant init -upgrade
terraform -chdir=./examples/decoupled/02-servers init -upgrade
terraform -chdir=./examples/decoupled/03-vpcpeering init -upgrade
terraform -chdir=./examples/decoupled/04-gpu-allocations init -upgrade
terraform -chdir=./examples/decoupled/04-gpus init -upgrade
terraform -chdir=./examples/decoupled/05-available-servers init -upgrade
terraform -chdir=./examples/decoupled/06-vf-interfaces init -upgrade
terraform -chdir=./examples/decoupled/07-vf-assign init -upgrade
```

