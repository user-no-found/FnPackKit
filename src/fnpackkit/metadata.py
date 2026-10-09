"""Read the tool identity from distribution metadata or its source manifest."""
from importlib.metadata import PackageNotFoundError, version
from pathlib import Path
import tomllib


def tool_version():
    try:
        return version("fnpackkit")
    except PackageNotFoundError:
        manifest = Path(__file__).resolve().parents[2] / "pyproject.toml"
        with manifest.open("rb") as stream:
            return tomllib.load(stream)["project"]["version"]
