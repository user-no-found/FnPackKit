"""Optional payload helper for Docker's pre-deployment capability check."""
from pathlib import Path

from .contracts import PayloadFile


def preflight_file() -> PayloadFile:
    """Include the standalone probe; invoke it from a package lifecycle hook."""
    return PayloadFile(Path(__file__).with_name("docker_preflight.py"),
                       "cmd/hooks/fnpack_docker.py", 0o755)
