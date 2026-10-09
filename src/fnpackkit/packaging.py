"""FPK-specific operations executed through a caller's BuildFlow context."""
import json
from pathlib import Path
import platform
import re

from buildflow.contracts import Artifact
from .verification import verify_package
from .manifest import validate_payload
from .metadata import tool_version
from .staging import create_stage, digest


def package(ctx, payload, fnpack="fnpack"):
    """Render, build and verify one declared payload and return publishable artifacts."""
    if platform.system() != "Linux":
        raise ValueError("FPK packaging requires native Linux")
    validate_payload(payload, ctx.project.data["app_id"])
    selected = ctx.project.data.get("version")
    if selected is not None and payload.values["VERSION"] != selected:
        raise ValueError("payload VERSION must equal the selected project.version")
    package_dir = ctx.work / "fpk" / payload.artifact_name.removesuffix(".fpk")
    stage = package_dir / "stage"
    expected = create_stage(payload, stage)
    version_output = ctx.runner.capture([str(fnpack), "--help"], cwd=stage)
    match = re.search(r"(?m)^Version\s+(\S+)", version_output)
    if not match:
        raise ValueError("cannot identify fnpack version")
    ctx.runner.run([str(fnpack), "build"], cwd=stage)
    built = stage / (payload.values["APP_ID"] + ".fpk")
    if built.is_symlink() or not built.is_file():
        raise ValueError("fnpack produced no regular FPK; inspect its diagnostic")
    actual = verify_package(built, stage, expected)
    artifact = package_dir / payload.artifact_name
    built.replace(artifact)
    checksum = digest(artifact)
    checksum_file = artifact.with_suffix(".fpk.sha256")
    checksum_file.write_text(f"{checksum}  {artifact.name}\n")
    report_file = artifact.with_suffix(".build.json")
    provenance = {"schema": 1, "app_id": ctx.project.data["app_id"],
                  "version": payload.values["VERSION"], "platform": payload.values["PLATFORM"],
                  "fnpackkit_version": tool_version(), "fnpack_version": match.group(1),
                  "fpk_sha256": checksum, "files": actual,
                  "build": ctx.metadata, "application": payload.metadata}
    report_file.write_text(json.dumps(provenance, ensure_ascii=False, indent=2) + "\n")
    ctx.metadata.setdefault("fpk_reports", {})[artifact.name] = str(report_file)
    return [Artifact(path, path.name) for path in (artifact, checksum_file)]
