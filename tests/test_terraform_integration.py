"""
Integration tests that hit the real Fabric API.

These are opt-in because they create/delete real resources.

Enable by exporting:
  - RUN_FABRICAPI_INTEGRATION=1
  - FABRIC_API_ENDPOINT (e.g. https://10.1.1.211 or http://host:port)
  - FABRICAPI_TEST_SERVERS (comma-separated; default is four hosts = 32 GPUs — override for fewer)
Optional:
  - FABRIC_NAME (defaults to provider default if unset)
  - FABRICAPI_TEST_MAX_GPUS (optional; must equal len(servers)*8 if set — otherwise derived)
  - FABRICAPI_TEST_SHARED (optional; "true" or "false" — sets Terraform var `shared` for GPU PATCH; default true)

If FABRIC_API_ENDPOINT is not provided, the integration test defaults to:
  https://10.20.0.41/

If FABRIC_NAME / FABRICAPI_TEST_SERVERS are not provided, the test defaults to:
  - FABRIC_NAME = 1SU-Fabric170619
  - FABRICAPI_TEST_SERVERS = hgx-su00-h00,hgx-su00-h01,hgx-su00-h02,hgx-su00-h03 (32 GPUs max)
"""

import base64
import os
import time
import warnings

import pytest


def _integration_enabled() -> bool:
    return os.getenv("RUN_FABRICAPI_INTEGRATION", "").lower() in {"1", "true", "yes"}

def _parse_servers(raw: str) -> list[str]:
    servers = [s.strip() for s in (raw or "").split(",") if s.strip()]
    return servers


@pytest.mark.integration
def test_all_terraform_commands_hit_api(docker_terraform):
    """
    Runs full Terraform lifecycle (init -> validate -> plan -> apply -> destroy)
    in the same container.

    This will:
    - Configure Terraform variables so provider hits the real API
      (FABRIC_API_ENDPOINT / FABRIC_NAME)
    - Run `terraform init`
    - Run `terraform validate`
    - Run `terraform plan` (hits API via data source)
    - Create a tenant (manage_tenant=true)
    - Allocate GPUs by assigning servers (fabricapi_tenant_servers)
    - Create VPC peering (TF_VAR_create_vpcpeering=true)
    - Destroy the tenant

    Default: 32 GPUs (four hosts). On success, emits a pytest warning + print so you can
    confirm VPC peering without the UI (use ``pytest -s`` to see print live).
    """
    if not _integration_enabled():
        pytest.skip("Set RUN_FABRICAPI_INTEGRATION=1 to enable integration tests.")

    # Default to the currently configured simulator API endpoint.
    # You can override this by setting FABRIC_API_ENDPOINT.
    endpoint = os.getenv("FABRIC_API_ENDPOINT", "https://10.20.0.41/").rstrip("/")
    if not endpoint.startswith("http://") and not endpoint.startswith("https://"):
        pytest.skip("FABRIC_API_ENDPOINT must start with http:// or https://")

    # Default: all four hosts => 32 GPUs (maximum capacity). Override to fewer servers if needed.
    servers_raw = os.getenv(
        "FABRICAPI_TEST_SERVERS",
        "hgx-su00-h00,hgx-su00-h01,hgx-su00-h02,hgx-su00-h03",
    )
    servers = _parse_servers(servers_raw)
    if not servers:
        pytest.skip("No servers configured for FABRICAPI_TEST_SERVERS.")
    if len(servers) < 1 or len(servers) > 4:
        pytest.skip("FABRICAPI_TEST_SERVERS must contain 1-4 servers.")

    # If this is set (e.g. in ~/.bashrc) to one host, you get 8 GPUs — not the 4-host default.
    if "FABRICAPI_TEST_SERVERS" in os.environ:
        print(
            "\n[pytest] FABRICAPI_TEST_SERVERS is set in your environment:\n"
            f"    {servers_raw!r}\n"
            f"    → {len(servers)} host(s) = {len(servers) * 8} GPUs.\n"
            "    For 32 GPUs (4 hosts), run:\n"
            "      unset FABRICAPI_TEST_SERVERS FABRICAPI_TEST_MAX_GPUS\n"
            '    or:\n'
            '      export FABRICAPI_TEST_SERVERS='
            '"hgx-su00-h00,hgx-su00-h01,hgx-su00-h02,hgx-su00-h03"\n',
            flush=True,
        )

    # Default to your currently configured simulator fabric.
    fabric = os.getenv("FABRIC_NAME", "1SU-Fabric170619")

    # VPC peering target fabric (must match tenant fabric for same-fabric peering).
    vpcpeering_target_fabric = os.getenv(
        "FABRICAPI_VPCPEERING_TARGET_FABRIC", fabric
    ).strip()
    if not vpcpeering_target_fabric:
        pytest.skip("FABRICAPI_VPCPEERING_TARGET_FABRIC must be non-empty.")

    # Unique tenant each run to avoid collisions.
    # Keep it <= 32 chars to satisfy tenant_name regex in the provider schema.
    tenant_name = os.getenv("FABRICAPI_TEST_TENANT_NAME") or f"pytest-{int(time.time())}"
    tenant_name = tenant_name[:32]

    # Tenant max_gpus_allowed must match listed servers: each server = 8 GPUs.
    # (A tenant cap of 16 with only one server in `servers` still allocates only 8 GPUs.)
    computed_max = len(servers) * 8
    override = os.getenv("FABRICAPI_TEST_MAX_GPUS", "").strip()
    if override:
        try:
            max_gpus_int = int(override)
        except ValueError:
            pytest.skip("FABRICAPI_TEST_MAX_GPUS must be an integer like 8, 16, 24, or 32.")
        if max_gpus_int != computed_max:
            pytest.skip(
                "FABRICAPI_TEST_MAX_GPUS must equal len(servers)*8 for integration tests. "
                f"Servers {servers} => {computed_max} GPUs; got FABRICAPI_TEST_MAX_GPUS={max_gpus_int}. "
                "Unset FABRICAPI_TEST_MAX_GPUS to auto-set from the server list."
            )
    else:
        max_gpus_int = computed_max

    if max_gpus_int not in {8, 16, 24, 32}:
        pytest.skip("Derived max GPUs must be one of: 8, 16, 24, 32 (1–4 servers).")

    # Shared flag must reach the provider (same as curl per-server "shared": true/false).
    # Pytest writes this file because TF_VAR_servers with commas breaks in some docker -e setups.
    shared_raw = os.getenv("FABRICAPI_TEST_SHARED", "true").strip().lower()
    if shared_raw not in {"true", "false", "1", "0", "yes", "no"}:
        pytest.skip('FABRICAPI_TEST_SHARED must be "true" or "false".')
    shared_bool = shared_raw in {"true", "1", "yes"}

    # HCL list for .tfvars (servers + max_gpus + shared must stay in sync with apply/destroy).
    servers_hcl = "[" + ",".join([f'"{s}"' for s in servers]) + "]"
    tfvars_gpu_block = (
        f"servers = {servers_hcl}\n"
        f"max_gpus_allowed = {max_gpus_int}\n"
        f"shared = {str(shared_bool).lower()}\n"
    )
    # Docker `-e TF_VAR_servers=[...]` breaks on commas on some setups; use an auto.tfvars file instead.
    tfvars_b64 = base64.b64encode(tfvars_gpu_block.encode("utf-8")).decode("ascii")

    print(
        f"\n[pytest] GPU allocation config: {len(servers)} host(s) => {max_gpus_int} GPUs; "
        f"servers={servers}; shared={shared_bool}\n"
        f"[pytest] If you see 8 GPUs only, unset FABRICAPI_TEST_SERVERS / FABRICAPI_TEST_MAX_GPUS "
        f"or export all four hosts.\n",
        flush=True,
    )

    # TF_VAR_* for everything except servers + max_gpus_allowed (those come from pytest-integration.auto.tfvars).
    env = {
        "FABRIC_API_ENDPOINT": endpoint,
        "FABRIC_NAME": fabric,
        "TF_VAR_api_endpoint": endpoint,
        "TF_VAR_fabric_name": fabric,
        "TF_VAR_tenant_name": tenant_name,
        "TF_VAR_tenant_description": "Managed by pytest",
        "TF_VAR_manage_tenant": "true",
        "TF_VAR_operation": "ADD",

        # Values for VPC peering Terraform resource.
        "TF_VAR_create_vpcpeering": "true",
        "TF_VAR_vpcpeering_target_fabric": vpcpeering_target_fabric,
        "TF_VAR_vpcpeering_delete_on_destroy": os.getenv(
            "FABRICAPI_VPCPEERING_DELETE_ON_DESTROY", "false"
        ).lower(),
        "TF_VAR_vpcpeering_name": os.getenv(
            "FABRICAPI_VPCPEERING_NAME",
            f"tf-vpcpeering-{tenant_name}-{int(time.time())}",
        )[:128],
    }

    # Optional: allow mock backends without seeded fabric to return 404 on list tenants.
    # Pass-through only when user explicitly sets it.
    allow_404_empty = os.getenv("FABRICAPI_ALLOW_FABRIC_404_EMPTY_LIST", "").strip()
    if allow_404_empty:
        env["FABRICAPI_ALLOW_FABRIC_404_EMPTY_LIST"] = allow_404_empty

    # Inject GPU vars via file so commas in list values are never split by docker -e.
    shell = (
        "set -u; "
        "rm -rf .terraform 2>/dev/null || true; "
        "rm -f .terraform.lock.hcl terraform.tfstate terraform.tfstate.backup tfplan terraform.tfvars terraform.tfvars.json pytest-integration.auto.tfvars 2>/dev/null || true; "
        f"printf '%s' '{tfvars_b64}' | base64 -d > pytest-integration.auto.tfvars; "
        "DESTROYED=0; "
        "cleanup() { "
        "  if [ \"$DESTROYED\" = \"0\" ]; then "
        "    terraform destroy -auto-approve -input=false -no-color >/dev/null 2>&1 || true; "
        "  fi; "
        "}; "
        "trap cleanup EXIT; "
        "terraform init -reconfigure -upgrade -input=false -no-color; "
        "terraform validate -no-color; "
        "terraform plan -detailed-exitcode -input=false -no-color -out=tfplan; "
        "plan_code=$?; "
        "if [ \"$plan_code\" -eq 1 ]; then exit 1; fi; "
        "terraform apply -auto-approve -input=false -no-color tfplan; "
        'echo "INTEGRATION pytest-integration.auto.tfvars (servers + max_gpus):"; cat pytest-integration.auto.tfvars; '
        f'eg=$(terraform output -raw expected_gpus_from_servers 2>/dev/null || true); '
        f'if [ "$eg" != "{max_gpus_int}" ]; then echo "expected_gpus_from_servers mismatch: got $eg want {max_gpus_int}"; exit 1; fi; '
        "tenant_id=\"$(terraform output -raw tenant_id 2>/dev/null || true)\"; "
        "servers_operation_id=\"$(terraform output -raw servers_operation_id 2>/dev/null || true)\"; "
        "vpcpeering_id=\"$(terraform output -raw vpcpeering_id 2>/dev/null || true)\"; "
        "if [ -z \"$tenant_id\" ]; then echo 'tenant_id output is empty'; exit 1; fi; "
        "if [ -z \"$servers_operation_id\" ]; then echo 'servers_operation_id output is empty'; exit 1; fi; "
        "if [ -z \"$vpcpeering_id\" ]; then echo 'vpcpeering_id output is empty'; exit 1; fi; "
        'echo "pytest confirmation: VPC peering is included in this test and completed (vpcpeering_id=$vpcpeering_id)"; '

        "terraform destroy -auto-approve -input=false -no-color; "
        "DESTROYED=1"
    )

    code, out, err = docker_terraform.run(shell=shell, env=env)
    assert code == 0, f"terraform lifecycle failed\nstdout:\n{out}\nstderr:\n{err}"

    # Pytest-visible confirmation (no UI): warning appears in pytest summary; print needs -s for live stream.
    if "FABRICAPI_TEST_SERVERS" in os.environ:
        hint = " (FABRICAPI_TEST_SERVERS was set — unset for 4-host / 32-GPU default)"
    else:
        hint = " (used default 4 hosts / 32 GPUs)"
    msg = (
        f"VPC peering is done — apply phase succeeded with non-empty vpcpeering_id; "
        f"{max_gpus_int} GPUs on {len(servers)} host(s).{hint}"
    )
    warnings.warn(f"[pytest] {msg}", UserWarning, stacklevel=1)
    print(f"\n[pytest] {msg}\n", flush=True)

