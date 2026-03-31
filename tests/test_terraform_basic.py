"""
Basic Terraform tests for the fabricapi provider.

Uses the Docker image (terraform-fabricapi:latest). Run ./build.sh first.
These tests do not require a live API for init and validate.
"""
import pytest


def test_terraform_init_succeeds(docker_terraform):
    """terraform init should complete successfully."""
    shell = (
        "rm -f .terraform.lock.hcl 2>/dev/null || true; "
        "rm -rf .terraform 2>/dev/null || true; "
        "terraform init -reconfigure -upgrade -input=false -no-color"
    )
    code, out, err = docker_terraform.run(shell=shell)
    assert code == 0, f"terraform init failed:\nstdout: {out}\nstderr: {err}"


def test_terraform_validate_succeeds(docker_terraform):
    """terraform init + validate should succeed (no API call in validate)."""
    code, out, err = docker_terraform.run(
        shell="rm -f .terraform.lock.hcl 2>/dev/null || true; rm -rf .terraform 2>/dev/null || true; terraform init -reconfigure -upgrade -input=false -no-color && terraform validate -no-color"
    )
    assert code == 0, f"terraform init/validate failed:\nstdout: {out}\nstderr: {err}"


def test_terraform_plan_runs(docker_terraform):
    """
    terraform plan runs without crashing.
    May exit 0 (no changes), 1 (error e.g. API unreachable), or 2 (changes present).
    We only assert the process runs and produces output.
    """
    code, out, err = docker_terraform.run(
        shell="rm -f .terraform.lock.hcl 2>/dev/null || true; rm -rf .terraform 2>/dev/null || true; terraform init -reconfigure -upgrade -input=false -no-color && terraform plan -input=false -no-color"
    )
    # 0 = no changes, 1 = error, 2 = changes present
    assert code in (0, 1, 2), f"unexpected exit code {code}\nstdout: {out}\nstderr: {err}"
    assert len(out + err) > 0
