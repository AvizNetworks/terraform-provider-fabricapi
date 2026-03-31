# Pytest automation for terraform-provider-fabricapi

These tests run Terraform **inside the provider Docker image** (same as `test.sh`). They do not require a live Fabric API for basic checks.

## Quick start

1. **Build the Docker image** (once):
   ```bash
   ./build.sh
   ```

2. **Install Python deps** (once):
   ```bash
   pip install -r tests/requirements.txt
   ```

3. **Run tests**:
   ```bash
   pytest tests/ -v
   ```
   Or from repo root:
   ```bash
   pytest
   ```

## What is tested

| Test | What it does |
|------|----------------|
| `test_terraform_init_succeeds` | `terraform init` exits 0 |
| `test_terraform_validate_succeeds` | `terraform init && terraform validate` exits 0 |
| `test_terraform_plan_runs` | `terraform init && terraform plan` runs (exit 0/1/2); no crash |
| `test_apply_then_destroy_hits_api` | (opt-in) `terraform apply` then `terraform destroy` against real API |

- **Init** and **validate** do not call the Fabric API.
- **Plan** will try to read the data source `fabricapi_tenants`; if the API is unreachable, plan exits with code 1 and the test still passes (we only assert the process ran).

## Skipping when Docker/image is missing

If Docker is not installed or the image `terraform-fabricapi:latest` is not built, pytest will **skip** the tests with a message. Run `./build.sh` to build the image.

## Optional: run with custom endpoint

To pass env vars into the container (e.g. for plan against your API), you can extend the fixture in `conftest.py` to add `-e FABRIC_API_ENDPOINT=...` when calling `docker run`, or set env in the test and pass `env=...` into the runner.

## Integration tests (apply/destroy)

These tests create and delete real resources. They are **skipped by default**.

Run them like this:

```bash
export RUN_FABRICAPI_INTEGRATION=1
export FABRIC_API_ENDPOINT="https://10.20.0.41"  # adjust to your API
export FABRIC_NAME="1SU-Fabric170619"            # optional
# Subprocess timeout for apply/destroy: default 60 minutes (3600s)
# export TERRAFORM_DOCKER_TIMEOUT_INTEGRATION_S=3600
# Default: four hosts => 32 GPUs (omit FABRICAPI_TEST_MAX_GPUS; derived as len(servers)*8)
# export FABRICAPI_TEST_SERVERS="hgx-su00-h00,hgx-su00-h01,hgx-su00-h02,hgx-su00-h03"
# Fewer hosts (e.g. 16 GPUs): FABRICAPI_TEST_SERVERS="hgx-su00-h00,hgx-su00-h01"
# Optional: FABRICAPI_TEST_MAX_GPUS must equal len(servers)*8 or pytest skips

pytest tests/ -v -m integration -s
```

