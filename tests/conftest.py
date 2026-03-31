"""
Pytest fixtures for Terraform provider tests.

Tests run Terraform via the Docker image (same as test.sh).
Build the image first: ./build.sh
"""
import subprocess
import os

import pytest

DOCKER_IMAGE = "terraform-fabricapi:latest"

DEFAULT_TERRAFORM_TIMEOUT_S = int(os.getenv("TERRAFORM_DOCKER_TIMEOUT_S", "120"))
# Integration: full lifecycle (init/validate/plan/apply/destroy) against the real API.
# Default limit is 60 minutes (3600s). Override with TERRAFORM_DOCKER_TIMEOUT_INTEGRATION_S if needed.
_INTEGRATION_DEFAULT_S = 60 * 60  # 60 minutes
INTEGRATION_TERRAFORM_TIMEOUT_S = int(
    os.getenv("TERRAFORM_DOCKER_TIMEOUT_INTEGRATION_S", str(_INTEGRATION_DEFAULT_S))
)

def _docker_cmd_prefix() -> list[str]:
    """
    Return the docker command prefix to use.

    - Prefer `docker` if the user can reach the Docker daemon.
    - If not, try `sudo -n docker` (non-interactive) so pytest doesn't hang.
    """
    try:
        subprocess.run(
            ["docker", "info"],
            capture_output=True,
            check=True,
            timeout=10,
            text=True,
        )
        return ["docker"]
    except (subprocess.CalledProcessError, FileNotFoundError, subprocess.TimeoutExpired):
        # Docker daemon isn't reachable for the current user. Prefer `sudo -n` to
        # keep things non-interactive; if that fails (password required), fall back
        # to interactive `sudo docker ...` so pytest doesn't just skip.
        try:
            subprocess.run(
                ["sudo", "-n", "docker", "info"],
                capture_output=True,
                check=True,
                timeout=10,
                text=True,
            )
            return ["sudo", "-n", "docker"]
        except Exception:
            return ["sudo", "docker"]


def _docker_available():
    """Check if Docker is available and image exists."""
    prefixes_to_try: list[list[str]] = []
    prefixes_to_try.append(["docker"])
    prefixes_to_try.append(["sudo", "-n", "docker"])
    prefixes_to_try.append(["sudo", "docker"])

    for prefix in prefixes_to_try:
        try:
            result = subprocess.run(
                prefix + ["image", "inspect", DOCKER_IMAGE],
                capture_output=True,
                timeout=10,
                text=True,
            )
            if result.returncode == 0:
                return True
        except (FileNotFoundError, subprocess.TimeoutExpired):
            continue

    return False


def _run_terraform_in_docker(*args, input_env=None, shell_cmd=None):
    """
    Run terraform (or a shell command) inside the provider Docker image.
    Returns (returncode, stdout, stderr).
    Use shell_cmd to run multiple commands in one container, e.g. "terraform init && terraform validate".
    """
    # NOTE: The Docker image sets ENTRYPOINT to /bin/sh.
    # We override it so tests can run `terraform ...` directly.
    #
    # Some simulator deployments only work with host networking (esp. for private IPs),
    # so we enable `--network host` automatically when targeting 10.20.0.41.
    env_endpoint = None
    if input_env and "FABRIC_API_ENDPOINT" in input_env:
        env_endpoint = input_env.get("FABRIC_API_ENDPOINT")
    if not env_endpoint:
        env_endpoint = os.getenv("FABRIC_API_ENDPOINT")

    # When FABRIC_API_ENDPOINT points at the *host* (localhost/127.0.0.1),
    # the container needs `--network host` to reach it on Linux.
    #
    # Keep prior behavior for the known simulator address as well.
    forced_host_network = os.getenv("TERRAFORM_DOCKER_FORCE_HOST_NETWORK", "").lower() in {
        "1", "true", "yes"
    }
    use_host_network = bool(env_endpoint and (
        "10.20.0.41" in env_endpoint
        or "localhost" in env_endpoint
        or "127.0.0.1" in env_endpoint
        or env_endpoint.endswith(":8787")  # common local dev port; harmless for others
    )) or forced_host_network

    prefix = _docker_cmd_prefix()
    cmd = [*prefix, "run", "--rm"]
    if use_host_network:
        # Append Docker flags after `docker run ...` so they are interpreted by Docker,
        # not by `sudo` (this matters when prefix is `sudo -n docker`).
        cmd.extend(["--network", "host"])
    cmd.extend([
        "-e", "TF_INPUT=0",
        "-w", "/workspace",
    ])
    if input_env:
        for k, v in (input_env or {}).items():
            cmd.extend(["-e", f"{k}={v}"])
    if shell_cmd:
        cmd.extend(["--entrypoint", "sh", DOCKER_IMAGE, "-c", shell_cmd])
    else:
        cmd.extend(["--entrypoint", "terraform", DOCKER_IMAGE])
        cmd.extend(args)

    timeout_s = DEFAULT_TERRAFORM_TIMEOUT_S
    if shell_cmd and ("terraform apply" in shell_cmd or "terraform destroy" in shell_cmd):
        timeout_s = INTEGRATION_TERRAFORM_TIMEOUT_S

    proc = subprocess.run(
        cmd,
        capture_output=True,
        text=True,
        timeout=timeout_s,
    )
    return proc.returncode, proc.stdout, proc.stderr


@pytest.fixture(scope="session")
def docker_terraform():
    """Session fixture: skip if Docker/image not available; else yield a runner."""
    if not _docker_available():
        pytest.skip(
            f"Docker or image '{DOCKER_IMAGE}' not available. Run ./build.sh first."
        )

    class Runner:
        @staticmethod
        def run(*args, env=None, shell=None):
            if shell is not None:
                return _run_terraform_in_docker(input_env=env, shell_cmd=shell)
            return _run_terraform_in_docker(*args, input_env=env)

    return Runner
