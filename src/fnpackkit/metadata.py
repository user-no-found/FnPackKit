"""Read the tool identity from distribution metadata or its source manifest."""
from importlib.metadata import PackageNotFoundError, version
from pathlib import Path
import tomllib


def tool_version():
    try:
        return version("fnpackkit")
    except PackageNotFoundError:
        manifest = Path(__file__).resolve().parents[2] / "pyproject.toml"
        if not manifest.is_file():
            raise ValueError("fnpackkit distribution metadata and source pyproject.toml are unavailable") from None
        with manifest.open("rb") as stream:
            return tomllib.load(stream)["project"]["version"]
